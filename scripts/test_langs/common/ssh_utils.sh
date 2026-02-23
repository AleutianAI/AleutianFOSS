#!/bin/bash
# SSH Utilities for CRS Integration Tests
# Handles SSH connection management, remote setup, and server lifecycle

# Setup ssh-agent for connection caching
setup_ssh_agent() {
    # Check if ssh-agent is already running with our key
    if ! ssh-add -l 2>/dev/null | grep -q "aleutiandevops_ansible_key"; then
        echo -e "${YELLOW}Setting up ssh-agent to cache passphrase...${NC}"
        eval "$(ssh-agent -s)" > /dev/null
        # Redirect stdin from terminal to allow passphrase input
        if ! ssh-add "$SSH_KEY" </dev/tty; then
            echo -e "${RED}Failed to add SSH key. Please check your passphrase.${NC}"
            return 1
        fi
        echo -e "${GREEN}SSH key added to agent${NC}"
    fi
}

# SSH command wrapper (uses multiplexed connection)
ssh_cmd() {
    ssh -i "$SSH_KEY" \
        -o StrictHostKeyChecking=no \
        -o ControlPath="$SSH_CONTROL_SOCKET" \
        -p "$REMOTE_PORT" "$REMOTE_USER@$REMOTE_HOST" "$@"
}

# Establish master SSH connection for multiplexing
establish_connection() {
    echo -e "${YELLOW}Establishing master SSH connection...${NC}"
    ssh -i "$SSH_KEY" -p "$REMOTE_PORT" \
        -o StrictHostKeyChecking=no \
        -o ControlMaster=auto \
        -o ControlPath="$SSH_CONTROL_SOCKET" \
        -o ControlPersist=10m \
        -fN "$REMOTE_USER@$REMOTE_HOST"
    echo -e "${GREEN}Master connection established (multiplexing enabled)${NC}"
}

# Close master SSH connection
close_connection() {
    ssh -O exit -o ControlPath="$SSH_CONTROL_SOCKET" "$REMOTE_USER@$REMOTE_HOST" 2>/dev/null || true
}

# Test SSH connection
test_ssh_connection() {
    echo -e "${YELLOW}Testing SSH connection to $REMOTE_USER@$REMOTE_HOST:$REMOTE_PORT${NC}"
    if ssh_cmd "echo 'SSH connection successful'"; then
        echo -e "${GREEN}SSH connection OK${NC}"
        return 0
    else
        echo -e "${RED}SSH connection failed${NC}"
        return 1
    fi
}

# Setup remote environment (sync project and build)
setup_remote() {
    echo -e "${YELLOW}Setting up remote environment...${NC}"

    # Create temp directory on remote
    ssh_cmd "mkdir -p ~/trace_test"

    # Wipe server log at the start of a new test run (first setup_remote call).
    # Subsequent server restarts (per-project) append via >>.
    if [ -z "$_TRACE_LOG_WIPED" ]; then
        ssh_cmd "truncate -s 0 ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null || true"
        export _TRACE_LOG_WIPED=1
    fi

    # Copy the project to analyze (if it's local Mac path)
    if [[ "$PROJECT_TO_ANALYZE" == /Users/* ]]; then
        echo "Syncing project to remote server..."
        local project_basename="$(basename "$PROJECT_TO_ANALYZE")"
        local remote_project="/home/$REMOTE_USER/trace_test/$project_basename"

        # Use rsync for efficient sync (uses multiplexed connection)
        rsync -az --delete -q --stats \
            --exclude '.git' \
            --exclude '.venv' \
            --exclude '__pycache__' \
            --exclude 'node_modules' \
            --exclude '.DS_Store' \
            -e "ssh -i $SSH_KEY -o StrictHostKeyChecking=no -o ControlPath=$SSH_CONTROL_SOCKET -p $REMOTE_PORT" \
            "$PROJECT_TO_ANALYZE/" "$REMOTE_USER@$REMOTE_HOST:$remote_project/" \
            | tail -3

        PROJECT_TO_ANALYZE="$remote_project"
        echo -e "${GREEN}Project synced to $remote_project${NC}"
    fi

    # Copy and build the trace server on remote
    echo "Building trace server on remote..."
    local local_repo="$(cd "$(dirname "$0")/.." && pwd)"

    # Sync the AleutianFOSS repo
    rsync -az --delete -q --stats \
        --exclude '.git' \
        --exclude '.venv' \
        --exclude '__pycache__' \
        --exclude 'bin' \
        --exclude '*.log' \
        --exclude 'trace_test_results*' \
        --exclude 'crs_test_results*' \
        --exclude 'node_modules' \
        --exclude '.DS_Store' \
        --exclude 'demo_data' \
        --exclude 'test_agent_data' \
        --exclude 'slides' \
        -e "ssh -i $SSH_KEY -o StrictHostKeyChecking=no -o ControlPath=$SSH_CONTROL_SOCKET -p $REMOTE_PORT" \
        "$local_repo/" "$REMOTE_USER@$REMOTE_HOST:~/trace_test/AleutianFOSS/" \
        | tail -3

    # Build on remote
    ssh_cmd "cd ~/trace_test/AleutianFOSS && go build -o bin/trace ./cmd/trace"

    echo -e "${GREEN}Remote environment ready${NC}"
}

# Check remote Ollama status
check_remote_ollama() {
    echo -e "${YELLOW}Checking Ollama on remote server...${NC}"

    if ! ssh_cmd "curl -s http://localhost:11434/api/tags" > /dev/null 2>&1; then
        echo -e "${RED}ERROR: Ollama is not running on remote server${NC}"
        echo "SSH into the server and start Ollama:"
        echo "  ssh -p $REMOTE_PORT $REMOTE_USER@$REMOTE_HOST"
        echo "  ollama serve"
        exit 1
    fi

    echo -e "${GREEN}✓ Ollama is running on remote server${NC}"

    # Get available models
    local models=$(ssh_cmd "curl -s http://localhost:11434/api/tags")

    # Check main agent model
    if ! echo "$models" | grep -q "$OLLAMA_MODEL"; then
        echo -e "${RED}ERROR: Model $OLLAMA_MODEL not found${NC}"
        exit 1
    fi
    echo -e "${GREEN}✓ Main Agent model available: $OLLAMA_MODEL${NC}"

    # Check router model
    if ! echo "$models" | grep -q "$ROUTER_MODEL"; then
        echo -e "${RED}ERROR: Router model $ROUTER_MODEL not found${NC}"
        exit 1
    fi
    echo -e "${GREEN}✓ Router model available: $ROUTER_MODEL${NC}"
}

# Start trace server on remote
start_trace_server() {
    echo -e "${YELLOW}Starting trace server on remote...${NC}"

    # Kill any existing trace server
    ssh_cmd "pkill -f 'bin/trace'" 2>/dev/null || true
    sleep 1

    # GR-40: Wipe stale graph cache to force rebuild with latest code
    # This ensures new features (like interface detection) are picked up
    echo "Wiping stale graph cache to force rebuild..."
    ssh_cmd "rm -f ~/trace_test/AleutianFOSS/*.db ~/trace_test/AleutianFOSS/graph_cache.json ~/trace_test/AleutianFOSS/*.gob 2>/dev/null" || true
    ssh_cmd "rm -rf ~/trace_test/AleutianFOSS/badger_* 2>/dev/null" || true
    # GR-17: Also wipe CRS persistence cache where graphs are actually stored
    ssh_cmd "rm -rf ~/.aleutian/crs/ 2>/dev/null" || true

    # Start the server in background
    ssh -f -i "$SSH_KEY" \
        -o StrictHostKeyChecking=no \
        -p "$REMOTE_PORT" "$REMOTE_USER@$REMOTE_HOST" \
        "cd ~/trace_test/AleutianFOSS && \
         OLLAMA_BASE_URL=http://localhost:11434 \
         OLLAMA_MODEL=$OLLAMA_MODEL \
         nohup ./bin/trace -with-context -with-tools >> trace_server.log 2>&1 &"

    sleep 2

    # Check if process started
    local server_pid
    server_pid=$(ssh_cmd "pgrep -f 'bin/trace'" 2>/dev/null || echo "")
    if [ -z "$server_pid" ]; then
        echo -e "${RED}ERROR: Failed to start trace server${NC}"
        ssh_cmd "cat ~/trace_test/AleutianFOSS/trace_server.log 2>/dev/null" || echo "(no log file)"
        return 1
    fi

    echo "Server started with PID: $server_pid"

    # Wait for server to be responding (basic connectivity)
    echo "Waiting for server to respond..."
    local responding=0
    for i in {1..15}; do
        echo -n "."
        sleep 1
        if ssh_cmd "curl -s http://localhost:8080/v1/codebuddy/health" > /dev/null 2>&1; then
            responding=1
            break
        fi
    done
    echo ""

    if [ $responding -eq 0 ]; then
        echo -e "${RED}ERROR: Trace server not responding after 15 seconds${NC}"
        ssh_cmd "tail -30 ~/trace_test/AleutianFOSS/trace_server.log" 2>/dev/null || true
        return 1
    fi

    # Wait for warmup to complete (poll /ready endpoint)
    # Model warmup takes 30-90 seconds for large models like glm-4.7-flash
    echo "Waiting for model warmup to complete (this may take 30-90 seconds)..."
    local ready=0
    for i in {1..120}; do
        # Check /ready endpoint - returns 200 when warmup complete, 503 when still warming
        local ready_status=$(ssh_cmd "curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/v1/codebuddy/ready" 2>/dev/null)
        if [ "$ready_status" = "200" ]; then
            ready=1
            break
        fi
        # Show progress every 10 seconds
        if [ $((i % 10)) -eq 0 ]; then
            echo "  Still warming up... (${i}s elapsed, status: $ready_status)"
        fi
        sleep 1
    done

    if [ $ready -eq 1 ]; then
        echo -e "${GREEN}Trace server is ready (warmup complete)${NC}"
        return 0
    else
        echo -e "${RED}ERROR: Model warmup did not complete after 120 seconds${NC}"
        ssh_cmd "tail -50 ~/trace_test/AleutianFOSS/trace_server.log" 2>/dev/null || true
        return 1
    fi
}

# Stop trace server on remote
stop_trace_server() {
    echo -e "${YELLOW}Stopping trace server...${NC}"
    ssh_cmd "pkill -f 'bin/trace'" 2>/dev/null || true
}
