#!/bin/bash
# Remote Integration Test Stack Orchestration
# Deploys 9 trace containers + test-runner to remote GPU server via SSH,
# runs YAML-driven integration tests, and captures results.
#
# Usage:
#   ./test/integration/run_stack.sh                           # Run all 9 projects
#   ./test/integration/run_stack.sh --projects hugo,flask      # Run specific projects
#   ./test/integration/run_stack.sh --main-model qwen3:14b     # Override model
#   ./test/integration/run_stack.sh --local                    # Run locally (no SSH)

set -euo pipefail

# ==============================================================================
# CONFIGURATION
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Remote server defaults (override via env vars)
REMOTE_HOST="${CRS_TEST_HOST:-10.0.0.250}"
REMOTE_PORT="${CRS_TEST_PORT:-13022}"
REMOTE_USER="${CRS_TEST_USER:-aleutiandevops}"
SSH_KEY="${SSH_KEY:-$HOME/.ssh/aleutiandevops_ansible_key}"

# Remote paths
REMOTE_WORK_DIR="/home/$REMOTE_USER/trace_integration_test"

# Model configuration
OLLAMA_MODEL="${OLLAMA_MODEL:-gpt-oss:20b}"
ROUTER_MODEL="${ROUTER_MODEL:-granite4:micro-h}"
PARAM_EXTRACTOR_MODEL="${PARAM_EXTRACTOR_MODEL:-ministral-3:3b}"

# Test codebases path on remote
CRS_TEST_CODEBASES="${CRS_TEST_CODEBASES:-/home/$REMOTE_USER/projects/crs_test_codebases}"

# Local mode
LOCAL_MODE=false

# Project filter
PROJECT_FILTER=""
FEATURE_FILTER=""

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Output files
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
RESULTS_FILE="/tmp/integration_test_results_${TIMESTAMP}.tap"

# ==============================================================================
# PARSE ARGUMENTS
# ==============================================================================

while [[ $# -gt 0 ]]; do
    case $1 in
        --projects)
            PROJECT_FILTER="$2"
            shift 2
            ;;
        --feature)
            FEATURE_FILTER="$2"
            shift 2
            ;;
        --main-model)
            OLLAMA_MODEL="$2"
            shift 2
            ;;
        --router-model)
            ROUTER_MODEL="$2"
            shift 2
            ;;
        --local)
            LOCAL_MODE=true
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --projects LIST    Comma-separated projects (e.g., hugo,flask)"
            echo "  --feature NAME     Feature filter (e.g., TOOL-HAPPY-HUGO)"
            echo "  --main-model MODEL Override main agent model (default: $OLLAMA_MODEL)"
            echo "  --router-model MODEL Override router model (default: $ROUTER_MODEL)"
            echo "  --local            Run locally instead of SSH to remote"
            echo "  -h, --help         Show this help"
            echo ""
            echo "Environment Variables:"
            echo "  CRS_TEST_HOST      Remote host (default: 10.0.0.250)"
            echo "  CRS_TEST_PORT      SSH port (default: 13022)"
            echo "  CRS_TEST_USER      SSH user (default: aleutiandevops)"
            echo "  SSH_KEY            Path to SSH key"
            echo "  CRS_TEST_CODEBASES Path to test codebases on remote"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# ==============================================================================
# HELPER FUNCTIONS
# ==============================================================================

ssh_cmd() {
    ssh -i "$SSH_KEY" \
        -o StrictHostKeyChecking=no \
        -o ConnectTimeout=10 \
        -p "$REMOTE_PORT" "$REMOTE_USER@$REMOTE_HOST" "$@"
}

rsync_to_remote() {
    local src="$1"
    local dst="$2"
    rsync -az --delete -q \
        --exclude '.git' \
        --exclude '.venv' \
        --exclude '__pycache__' \
        --exclude 'node_modules' \
        --exclude '.DS_Store' \
        --exclude '*.log' \
        --exclude 'bin' \
        -e "ssh -i $SSH_KEY -o StrictHostKeyChecking=no -p $REMOTE_PORT" \
        "$src" "$REMOTE_USER@$REMOTE_HOST:$dst"
}

# ==============================================================================
# LOCAL MODE
# ==============================================================================

run_local() {
    echo -e "${GREEN}Running integration tests locally...${NC}"

    local compose_file="$SCRIPT_DIR/podman-compose.test.yml"

    # Build images
    echo -e "${YELLOW}Building trace image...${NC}"
    podman build -t aleutian-trace:latest -f "$REPO_ROOT/services/trace/Dockerfile" "$REPO_ROOT"

    echo -e "${YELLOW}Building test-runner image...${NC}"
    podman build -t test-runner:latest -f "$SCRIPT_DIR/Dockerfile.test-runner" "$SCRIPT_DIR"

    # Set env vars for compose
    export OLLAMA_MODEL ROUTER_MODEL PARAM_EXTRACTOR_MODEL
    export CRS_TEST_CODEBASES="${CRS_TEST_CODEBASES:-$HOME/projects/crs_test_codebases}"
    export PROJECT_FILTER FEATURE_FILTER

    echo -e "${YELLOW}Starting test stack...${NC}"
    podman-compose -f "$compose_file" up \
        --abort-on-container-exit \
        --exit-code-from test-runner \
        2>&1 | tee "$RESULTS_FILE"

    local exit_code=${PIPESTATUS[0]}

    echo -e "${YELLOW}Cleaning up...${NC}"
    podman-compose -f "$compose_file" down --volumes 2>/dev/null || true

    return "$exit_code"
}

# ==============================================================================
# REMOTE MODE
# ==============================================================================

run_remote() {
    echo -e "${GREEN}Deploying integration test stack to ${REMOTE_HOST}...${NC}"

    # Test SSH connectivity
    echo -e "${YELLOW}Testing SSH connection...${NC}"
    if ! ssh_cmd "echo 'SSH OK'"; then
        echo -e "${RED}SSH connection failed to ${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_PORT}${NC}"
        exit 1
    fi

    # Check Ollama is running
    echo -e "${YELLOW}Checking Ollama...${NC}"
    if ! ssh_cmd "curl -sf http://localhost:11434/api/tags > /dev/null"; then
        echo -e "${RED}Ollama not running on remote server${NC}"
        exit 1
    fi
    echo -e "${GREEN}Ollama is running${NC}"

    # Check test codebases exist
    echo -e "${YELLOW}Checking test codebases...${NC}"
    if ! ssh_cmd "test -d $CRS_TEST_CODEBASES"; then
        echo -e "${RED}Test codebases not found at $CRS_TEST_CODEBASES${NC}"
        echo "Clone them with: scripts/test_langs/common/project_utils.sh"
        exit 1
    fi

    # Create remote working directory
    ssh_cmd "mkdir -p $REMOTE_WORK_DIR"

    # Sync repository to remote
    echo -e "${YELLOW}Syncing repository to remote...${NC}"
    rsync_to_remote "$REPO_ROOT/" "$REMOTE_WORK_DIR/AleutianFOSS/"
    echo -e "${GREEN}Repository synced${NC}"

    # Build images on remote
    echo -e "${YELLOW}Building trace image on remote (this may take a few minutes)...${NC}"
    ssh_cmd "cd $REMOTE_WORK_DIR/AleutianFOSS && \
        podman build -t aleutian-trace:latest -f services/trace/Dockerfile ."

    echo -e "${YELLOW}Building test-runner image on remote...${NC}"
    ssh_cmd "cd $REMOTE_WORK_DIR/AleutianFOSS && \
        podman build -t test-runner:latest -f test/integration/Dockerfile.test-runner test/integration/"

    # Run the stack
    echo -e "${YELLOW}Starting test stack on remote...${NC}"
    # For containers running on the same host as Ollama, use localhost.
    # Podman on Linux uses host network differently than macOS.
    ssh_cmd "cd $REMOTE_WORK_DIR/AleutianFOSS && \
        OLLAMA_MODEL='$OLLAMA_MODEL' \
        ROUTER_MODEL='$ROUTER_MODEL' \
        PARAM_EXTRACTOR_MODEL='$PARAM_EXTRACTOR_MODEL' \
        CRS_TEST_CODEBASES='$CRS_TEST_CODEBASES' \
        PROJECT_FILTER='$PROJECT_FILTER' \
        FEATURE_FILTER='$FEATURE_FILTER' \
        podman-compose -f test/integration/podman-compose.test.yml up \
            --abort-on-container-exit \
            --exit-code-from test-runner" \
        2>&1 | tee "$RESULTS_FILE"

    local exit_code=${PIPESTATUS[0]}

    # Cleanup remote containers
    echo -e "${YELLOW}Cleaning up remote containers...${NC}"
    ssh_cmd "cd $REMOTE_WORK_DIR/AleutianFOSS && \
        podman-compose -f test/integration/podman-compose.test.yml down --volumes" 2>/dev/null || true

    return "$exit_code"
}

# ==============================================================================
# MAIN
# ==============================================================================

echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}  Container-Based Integration Test Stack${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""
echo "  Mode:     $([ "$LOCAL_MODE" = true ] && echo "local" || echo "remote ($REMOTE_HOST)")"
echo "  Model:    $OLLAMA_MODEL"
echo "  Router:   $ROUTER_MODEL"
echo "  Projects: ${PROJECT_FILTER:-all}"
echo "  Feature:  ${FEATURE_FILTER:-all}"
echo "  Results:  $RESULTS_FILE"
echo ""

if [ "$LOCAL_MODE" = true ]; then
    run_local
else
    run_remote
fi

exit_code=$?

echo ""
if [ "$exit_code" -eq 0 ]; then
    echo -e "${GREEN}All tests passed.${NC}"
else
    echo -e "${RED}Some tests failed. See: $RESULTS_FILE${NC}"
fi

exit "$exit_code"
