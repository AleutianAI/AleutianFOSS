#!/bin/bash
# Project Path Utilities for Multi-Language Test Suite
# Resolves project paths for Go, Python, JavaScript, TypeScript
# Handles cloning open-source projects for integration testing

# ==============================================================================
# PROJECT METADATA - Integration Test Projects
# ==============================================================================

# Base path for cloned test projects
TEST_PROJECTS_BASE="${HOME}/projects/crs_test_codebases"

# Get repository URL for a project (bash 3.2 compatible)
get_project_repo() {
    local project_key="$1"
    case "$project_key" in
        go/hugo) echo "https://github.com/gohugoio/hugo" ;;
        python/flask) echo "https://github.com/pallets/flask" ;;
        javascript/express) echo "https://github.com/expressjs/express" ;;
        typescript/nestjs) echo "https://github.com/nestjs/nest" ;;
        *) echo "" ;;
    esac
}

# Get release tag for a project (bash 3.2 compatible)
get_project_tag() {
    local project_key="$1"
    case "$project_key" in
        go/hugo) echo "v0.139.4" ;;
        python/flask) echo "3.1.0" ;;
        javascript/express) echo "4.21.2" ;;
        typescript/nestjs) echo "v10.4.15" ;;
        *) echo "" ;;
    esac
}

# Get estimated size for a project (bash 3.2 compatible)
get_project_size() {
    local project_key="$1"
    case "$project_key" in
        go/hugo) echo "65K" ;;
        python/flask) echo "15K" ;;
        javascript/express) echo "6K" ;;
        typescript/nestjs) echo "50K" ;;
        *) echo "unknown" ;;
    esac
}

# List all known project keys
list_known_projects() {
    echo "go/hugo"
    echo "python/flask"
    echo "javascript/express"
    echo "typescript/nestjs"
}

# Get the absolute path to the test_langs directory
get_test_langs_root() {
    local script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
    echo "$script_dir"
}

# Resolve project root path based on language and project name
# Usage: get_project_root "go" "orchestrator"
# Returns: absolute path to the project
get_project_root() {
    local language="$1"
    local project_name="$2"

    if [ -z "$language" ] || [ -z "$project_name" ]; then
        echo "ERROR: get_project_root requires language and project_name" >&2
        return 1
    fi

    local test_langs_root="$(get_test_langs_root)"
    local project_path="$test_langs_root/test_projects/$language/$project_name"

    # Check if project exists
    if [ ! -d "$project_path" ]; then
        echo "ERROR: Project not found: $project_path" >&2
        return 1
    fi

    echo "$project_path"
}

# List all available projects for a language
# Usage: list_projects "go"
list_projects() {
    local language="$1"
    local test_langs_root="$(get_test_langs_root)"
    local lang_dir="$test_langs_root/test_projects/$language"

    if [ ! -d "$lang_dir" ]; then
        echo "ERROR: Language directory not found: $lang_dir" >&2
        return 1
    fi

    # List subdirectories (projects)
    find "$lang_dir" -maxdepth 1 -mindepth 1 -type d -exec basename {} \;
}

# Validate project exists
# Usage: validate_project "python" "flask_api"
validate_project() {
    local language="$1"
    local project_name="$2"

    local project_path="$(get_project_root "$language" "$project_name")"
    local result=$?

    if [ $result -eq 0 ]; then
        return 0
    else
        return 1
    fi
}

# Get project file count (for verification)
# Usage: get_project_file_count "python" "flask_api"
get_project_file_count() {
    local language="$1"
    local project_name="$2"

    local project_path="$(get_project_root "$language" "$project_name")"
    if [ $? -ne 0 ]; then
        return 1
    fi

    # Count source files based on language
    case "$language" in
        go)
            find "$project_path" -name "*.go" -type f | wc -l
            ;;
        python)
            find "$project_path" -name "*.py" -type f | wc -l
            ;;
        javascript)
            find "$project_path" -name "*.js" -o -name "*.jsx" -type f | wc -l
            ;;
        typescript)
            find "$project_path" -name "*.ts" -o -name "*.tsx" -type f | wc -l
            ;;
        *)
            echo "0"
            ;;
    esac
}

# Get language from test YAML metadata
# Usage: extract_language_from_yaml "features/GR-36/go.yml"
extract_language_from_yaml() {
    local yaml_file="$1"

    if [ ! -f "$yaml_file" ]; then
        echo "ERROR: YAML file not found: $yaml_file" >&2
        return 1
    fi

    # Extract language field from metadata (requires yq)
    if command -v yq >/dev/null 2>&1; then
        yq eval '.metadata.language' "$yaml_file"
    else
        # Fallback: parse filename (e.g., "go.yml" -> "go")
        basename "$yaml_file" .yml
    fi
}

# Get project name from test YAML metadata
# Usage: extract_project_from_yaml "features/GR-36/go.yml"
extract_project_from_yaml() {
    local yaml_file="$1"

    if [ ! -f "$yaml_file" ]; then
        echo "ERROR: YAML file not found: $yaml_file" >&2
        return 1
    fi

    # Extract project field from metadata (requires yq)
    if command -v yq >/dev/null 2>&1; then
        yq eval '.metadata.project' "$yaml_file"
    else
        echo "ERROR: yq required to extract project from YAML" >&2
        return 1
    fi
}

# Sync project to remote server (for remote testing)
# Usage: sync_project_to_remote "python" "flask_api" "user@host" "remote_path"
sync_project_to_remote() {
    local language="$1"
    local project_name="$2"
    local remote_user_host="$3"
    local remote_base_path="$4"

    local project_path="$(get_project_root "$language" "$project_name")"
    if [ $? -ne 0 ]; then
        echo "ERROR: Cannot sync non-existent project" >&2
        return 1
    fi

    local remote_project="$remote_base_path/$language/$project_name"

    echo "Syncing $language/$project_name to $remote_user_host:$remote_project..."

    # Use rsync for efficient sync
    rsync -az --delete -q \
        --exclude '.git' \
        --exclude '.venv' \
        --exclude '__pycache__' \
        --exclude 'node_modules' \
        --exclude '.DS_Store' \
        "$project_path/" "$remote_user_host:$remote_project/"

    if [ $? -eq 0 ]; then
        echo "✓ Project synced successfully"
        echo "$remote_project"
        return 0
    else
        echo "✗ Project sync failed"
        return 1
    fi
}

# ==============================================================================
# PROJECT SETUP & CLONING - For Integration Tests
# ==============================================================================

# Get the clone path for an integration test project
# Usage: get_project_clone_path "go" "hugo"
# Returns: $HOME/projects/crs_test_codebases/go/hugo
get_project_clone_path() {
    local language="$1"
    local project_name="$2"

    if [ -z "$language" ] || [ -z "$project_name" ]; then
        echo "ERROR: get_project_clone_path requires language and project_name" >&2
        return 1
    fi

    echo "${TEST_PROJECTS_BASE}/${language}/${project_name}"
}

# Check if a project is already cloned
# Usage: is_project_cloned "go" "hugo"
is_project_cloned() {
    local language="$1"
    local project_name="$2"

    local clone_path="$(get_project_clone_path "$language" "$project_name")"

    if [ -d "$clone_path/.git" ]; then
        return 0  # True - project is cloned
    else
        return 1  # False - project not cloned
    fi
}

# Setup (clone) a test project from open source repos
# Usage: setup_test_project "go" "hugo"
# Returns: path to cloned project on success
setup_test_project() {
    local language="$1"
    local project_name="$2"
    local force_reclone="${3:-false}"

    if [ -z "$language" ] || [ -z "$project_name" ]; then
        echo -e "${RED}ERROR: setup_test_project requires language and project_name${NC}" >&2
        return 1
    fi

    local project_key="${language}/${project_name}"
    local repo_url="$(get_project_repo "$project_key")"
    local release_tag="$(get_project_tag "$project_key")"
    local clone_path="$(get_project_clone_path "$language" "$project_name")"

    # Validate project exists in metadata
    if [ -z "$repo_url" ]; then
        echo -e "${RED}ERROR: Unknown project: $project_key${NC}" >&2
        echo "Available projects:" >&2
        list_known_projects | while read key; do
            echo "  - $key" >&2
        done
        return 1
    fi

    # Check if already cloned
    if is_project_cloned "$language" "$project_name" && [ "$force_reclone" != "true" ]; then
        echo -e "${GREEN}✓ Project already cloned: $clone_path${NC}"

        # Verify it's on the correct tag
        local current_tag=$(cd "$clone_path" && git describe --tags --exact-match 2>/dev/null || echo "unknown")
        if [ "$current_tag" != "$release_tag" ]; then
            echo -e "${YELLOW}⚠ Warning: Project is on tag '$current_tag', expected '$release_tag'${NC}"
            echo "  Run with force_reclone=true to re-clone at correct tag"
        fi

        echo "$clone_path"
        return 0
    fi

    # Create base directory if needed
    mkdir -p "$(dirname "$clone_path")"

    echo -e "${CYAN}Cloning $project_key from GitHub...${NC}"
    echo "  Repo: $repo_url"
    echo "  Tag: $release_tag"
    echo "  Path: $clone_path"

    # Remove existing directory if force reclone
    if [ "$force_reclone" = "true" ] && [ -d "$clone_path" ]; then
        echo "  Removing existing clone..."
        rm -rf "$clone_path"
    fi

    # Clone repository
    if ! git clone --quiet --depth 1 --branch "$release_tag" "$repo_url" "$clone_path" 2>&1; then
        # Fallback: clone full repo and checkout tag (for older tags not available via --branch)
        echo -e "${YELLOW}  Shallow clone failed, trying full clone...${NC}"
        if ! git clone --quiet "$repo_url" "$clone_path" 2>&1; then
            echo -e "${RED}✗ Failed to clone repository${NC}" >&2
            return 1
        fi

        # Checkout the specific tag
        if ! (cd "$clone_path" && git checkout --quiet "$release_tag" 2>&1); then
            echo -e "${RED}✗ Failed to checkout tag $release_tag${NC}" >&2
            rm -rf "$clone_path"
            return 1
        fi
    fi

    # Verify clone succeeded
    if [ ! -d "$clone_path/.git" ]; then
        echo -e "${RED}✗ Clone verification failed${NC}" >&2
        return 1
    fi

    # Get actual tag for verification
    local actual_tag=$(cd "$clone_path" && git describe --tags --exact-match 2>/dev/null || echo "detached")

    echo -e "${GREEN}✓ Successfully cloned $project_name${NC}"
    echo "  Tag: $actual_tag"
    echo "  Files: $(find "$clone_path" -type f -name "*.$language" 2>/dev/null | wc -l | tr -d ' ')"
    echo "  Size: $(get_project_size "$project_key") LOC (estimated)"

    echo "$clone_path"
    return 0
}

# Setup all integration test projects (Phase 1: Hugo, Flask, Express, NestJS)
# Usage: setup_all_test_projects [force_reclone]
setup_all_test_projects() {
    local force_reclone="${1:-false}"

    echo -e "${BLUE}Setting up integration test projects...${NC}"
    echo ""

    local projects=(
        "go hugo"
        "python flask"
        "javascript express"
        "typescript nestjs"
    )

    local success_count=0
    local fail_count=0

    for project_spec in "${projects[@]}"; do
        local lang=$(echo "$project_spec" | awk '{print $1}')
        local name=$(echo "$project_spec" | awk '{print $2}')

        if setup_test_project "$lang" "$name" "$force_reclone"; then
            ((success_count++))
        else
            ((fail_count++))
            echo -e "${RED}✗ Failed to setup $lang/$name${NC}"
        fi
        echo ""
    done

    echo -e "${BLUE}═══════════════════════════════════════${NC}"
    echo -e "${GREEN}✓ Successfully setup: $success_count projects${NC}"
    if [ $fail_count -gt 0 ]; then
        echo -e "${RED}✗ Failed: $fail_count projects${NC}"
    fi
    echo ""

    if [ $fail_count -eq 0 ]; then
        return 0
    else
        return 1
    fi
}

# Verify parser compatibility for a cloned project
# Usage: verify_parser_compatibility "go" "hugo"
verify_parser_compatibility() {
    local language="$1"
    local project_name="$2"

    local clone_path="$(get_project_clone_path "$language" "$project_name")"

    if ! is_project_cloned "$language" "$project_name"; then
        echo -e "${RED}ERROR: Project not cloned: $language/$project_name${NC}" >&2
        return 1
    fi

    echo -e "${CYAN}Verifying parser compatibility for $language/$project_name...${NC}"

    # Try to parse a sample file from the project
    case "$language" in
        go)
            # Find a Go file and try to parse it
            local sample_file=$(find "$clone_path" -name "*.go" -type f | head -1)
            if [ -z "$sample_file" ]; then
                echo -e "${RED}✗ No Go files found in project${NC}"
                return 1
            fi

            # TODO: Add actual parser test once CLI supports it
            echo "  Sample file: $sample_file"
            echo -e "${GREEN}✓ Go files found (parser compatibility TBD)${NC}"
            return 0
            ;;
        python)
            local sample_file=$(find "$clone_path" -name "*.py" -type f | head -1)
            if [ -z "$sample_file" ]; then
                echo -e "${RED}✗ No Python files found in project${NC}"
                return 1
            fi
            echo "  Sample file: $sample_file"
            echo -e "${GREEN}✓ Python files found (parser compatibility TBD)${NC}"
            return 0
            ;;
        javascript|typescript)
            local ext="js"
            [ "$language" = "typescript" ] && ext="ts"

            local sample_file=$(find "$clone_path" -name "*.$ext" -type f | head -1)
            if [ -z "$sample_file" ]; then
                echo -e "${RED}✗ No $language files found in project${NC}"
                return 1
            fi
            echo "  Sample file: $sample_file"
            echo -e "${GREEN}✓ $language files found (parser compatibility TBD)${NC}"
            return 0
            ;;
        *)
            echo -e "${RED}ERROR: Unsupported language: $language${NC}"
            return 1
            ;;
    esac
}

# Self-test function
test_project_utils() {
    echo "Testing project_utils.sh..."
    echo ""

    echo "Test 1: get_test_langs_root"
    local root="$(get_test_langs_root)"
    echo "  Root: $root"
    echo ""

    echo "Test 2: get_project_clone_path"
    local hugo_path="$(get_project_clone_path "go" "hugo")"
    echo "  Hugo clone path: $hugo_path"
    echo ""

    echo "Test 3: is_project_cloned go hugo"
    if is_project_cloned "go" "hugo"; then
        echo "  ✓ Hugo is cloned"
    else
        echo "  ✗ Hugo is not cloned"
    fi
    echo ""

    echo "Test 4: Project metadata"
    echo "  Hugo repo: $(get_project_repo 'go/hugo')"
    echo "  Hugo tag: $(get_project_tag 'go/hugo')"
    echo "  Hugo size: $(get_project_size 'go/hugo') LOC"
    echo ""

    echo "All tests complete"
    echo ""
    echo "To setup integration test projects, run:"
    echo "  ./scripts/test_langs/common/project_utils.sh setup"
}

# ==============================================================================
# CLI INTERFACE - Run script directly to setup projects
# ==============================================================================

# If script is run directly, provide CLI interface
if [ "${BASH_SOURCE[0]}" = "${0}" ]; then
    case "${1:-test}" in
        test)
            test_project_utils
            ;;
        setup)
            echo "Setting up all integration test projects..."
            setup_all_test_projects "${2:-false}"
            ;;
        setup-force)
            echo "Force re-cloning all integration test projects..."
            setup_all_test_projects "true"
            ;;
        clone)
            if [ -z "$2" ] || [ -z "$3" ]; then
                echo "Usage: $0 clone <language> <project>"
                echo "Example: $0 clone go hugo"
                exit 1
            fi
            setup_test_project "$2" "$3" "false"
            ;;
        verify)
            if [ -z "$2" ] || [ -z "$3" ]; then
                echo "Usage: $0 verify <language> <project>"
                echo "Example: $0 verify go hugo"
                exit 1
            fi
            verify_parser_compatibility "$2" "$3"
            ;;
        list)
            echo "Available integration test projects:"
            echo ""
            while read key; do
                lang=$(echo "$key" | cut -d'/' -f1)
                name=$(echo "$key" | cut -d'/' -f2)
                repo="$(get_project_repo "$key")"
                tag="$(get_project_tag "$key")"
                size="$(get_project_size "$key")"

                printf "  %-20s  %-60s  %-15s  %s\n" "$key" "$repo" "$tag" "$size LOC"
            done < <(list_known_projects)
            echo ""
            ;;
        help)
            echo "Project Utilities - Integration Test Project Management"
            echo ""
            echo "Usage: $0 <command> [args]"
            echo ""
            echo "Commands:"
            echo "  test               Run self-tests"
            echo "  setup              Setup all integration test projects (Hugo, Flask, Express, NestJS)"
            echo "  setup-force        Force re-clone all projects"
            echo "  clone <lang> <proj>  Clone a specific project (e.g., clone go hugo)"
            echo "  verify <lang> <proj> Verify parser compatibility for a project"
            echo "  list               List all available projects"
            echo "  help               Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0 setup              # Clone all 4 Phase 1 projects"
            echo "  $0 clone go hugo      # Clone only Hugo"
            echo "  $0 verify python flask # Verify Flask parser works"
            echo ""
            ;;
        *)
            echo "Unknown command: $1"
            echo "Run '$0 help' for usage information"
            exit 1
            ;;
    esac
fi
