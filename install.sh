#!/usr/bin/env bash

set -e

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Detect OS
detect_os() {
    case "$(uname -s)" in
        Linux*)     OS="Linux";;
        Darwin*)    OS="macOS";;
        CYGWIN*|MINGW*|MSYS*)    OS="Windows";;
        *)          OS="Unknown";;
    esac
}

# Set platform-specific variables
set_platform_vars() {
    detect_os
    
    case $OS in
        Linux)
            BIN_DIR="$HOME/.local/bin"
            TEMP_DIR="/tmp"
            NEEDS_SUDO=false
            EXE_EXT=""
            SOCK_EXT=".sock"
            ;;
        macOS)
            BIN_DIR="/usr/local/bin"
            TEMP_DIR="/tmp"
            NEEDS_SUDO=true
            EXE_EXT=""
            SOCK_EXT=".sock"
            ;;
        Windows)
            # Use AppData for Windows
            BIN_DIR="${APPDATA}/mosaic/bin"
            TEMP_DIR="${TEMP:-/tmp}"
            NEEDS_SUDO=false
            EXE_EXT=".exe"
            SOCK_EXT=""
            ;;
        *)
            echo -e "${RED}✗ Unsupported operating system${NC}"
            exit 1
            ;;
    esac
    
    CLI_BIN="mos${EXE_EXT}"
    DAEMON_BIN="mosaicd${EXE_EXT}"
    PID_FILE="${TEMP_DIR}/mosaicd.pid"
    LOG_FILE="${TEMP_DIR}/mosaicd.log"
    SOCK_FILE="${TEMP_DIR}/mosaicd${SOCK_EXT}"
}

# Check if daemon is running
is_daemon_running() {
    if [ "$OS" = "Windows" ]; then
        tasklist 2>/dev/null | grep -i "mosaicd.exe" > /dev/null 2>&1
    else
        pgrep -f "mosaicd" > /dev/null 2>&1
    fi
}

# Stop daemon (robust version)
stop_daemon() {
    echo ""
    echo "Checking for running daemons..."
    local stopped=false
    local attempts=0
    local max_attempts=3
    
    while [ $attempts -lt $max_attempts ]; do
        # Try PID file first
        if [ -f "${PID_FILE}" ]; then
            local pid=$(cat "${PID_FILE}" 2>/dev/null || echo "")
            if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
                echo "Stopping daemon (PID: $pid)..."
                kill "$pid" 2>/dev/null || true
                sleep 1
                
                # Check if still alive, use SIGKILL
                if kill -0 "$pid" 2>/dev/null; then
                    echo "Process still alive, using SIGKILL..."
                    kill -9 "$pid" 2>/dev/null || true
                    sleep 1
                fi
                stopped=true
            fi
            rm -f "${PID_FILE}"
        fi
        
        # Fallback: kill by process name
        if is_daemon_running; then
            echo "Found running mosaicd processes (attempt $((attempts+1))/$max_attempts)..."
            if [ "$OS" = "Windows" ]; then
                taskkill /F /IM "mosaicd.exe" 2>/dev/null || true
            else
                # Try graceful kill first
                pkill -TERM -f "mosaicd" 2>/dev/null || true
                sleep 2
                
                # Force kill if still running
                if pgrep -f "mosaicd" > /dev/null 2>&1; then
                    echo "Using SIGKILL..."
                    pkill -9 -f "mosaicd" 2>/dev/null || true
                    sleep 1
                fi
            fi
            stopped=true
        else
            # No processes found, we're done
            break
        fi
        
        attempts=$((attempts+1))
        
        # Check if we succeeded
        if ! is_daemon_running; then
            break
        fi
        
        if [ $attempts -lt $max_attempts ]; then
            echo "Retrying..."
            sleep 1
        fi
    done
    
    # Clean up socket
    rm -f "${SOCK_FILE}"
    
    # Final verification
    if is_daemon_running; then
        echo -e "${RED}⚠ Warning: mosaicd processes still running after $max_attempts attempts${NC}"
        echo ""
        echo "Running processes:"
        if [ "$OS" = "Windows" ]; then
            tasklist | grep -i "mosaicd.exe" || true
        else
            ps aux | grep mosaicd | grep -v grep || true
        fi
        echo ""
        echo -e "${YELLOW}You may need to manually kill these processes:${NC}"
        if [ "$OS" = "Windows" ]; then
            echo "  taskkill /F /IM mosaicd.exe"
        else
            echo "  pkill -9 -f mosaicd"
        fi
        return 1
    else
        if [ "$stopped" = true ]; then
            echo -e "${GREEN}✓ Daemon stopped${NC}"
        else
            echo "No daemon was running"
        fi
        return 0
    fi
}

# Build binaries
build_binaries() {
    echo "Building binaries for $OS..."
    mkdir -p bin
    
    if [ "$OS" = "Windows" ]; then
        GOOS=windows GOARCH=amd64 go build -o "bin/${CLI_BIN}" ./cmd/mosaic-cli
        GOOS=windows GOARCH=amd64 go build -o "bin/${DAEMON_BIN}" ./cmd/mosaic-node
    else
        go build -o "bin/${CLI_BIN}" ./cmd/mosaic-cli
        go build -o "bin/${DAEMON_BIN}" ./cmd/mosaic-node
    fi
    
    echo -e "${GREEN}✓ Build complete!${NC}"
}

# Install binaries
install_binaries() {
    echo ""
    echo "Installing to ${BIN_DIR}..."
    
    # Create bin directory if it doesn't exist
    if [ "$NEEDS_SUDO" = true ]; then
        sudo mkdir -p "${BIN_DIR}"
        sudo cp "bin/${CLI_BIN}" "${BIN_DIR}/"
        sudo cp "bin/${DAEMON_BIN}" "${BIN_DIR}/"
        sudo chmod +x "${BIN_DIR}/${CLI_BIN}"
        sudo chmod +x "${BIN_DIR}/${DAEMON_BIN}"
    else
        mkdir -p "${BIN_DIR}"
        cp "bin/${CLI_BIN}" "${BIN_DIR}/"
        cp "bin/${DAEMON_BIN}" "${BIN_DIR}/"
        chmod +x "${BIN_DIR}/${CLI_BIN}" 2>/dev/null || true
        chmod +x "${BIN_DIR}/${DAEMON_BIN}" 2>/dev/null || true
    fi
    
    echo -e "${GREEN}✓ Installed successfully!${NC}"
    
    # Check if BIN_DIR is in PATH
    if [ "$OS" != "macOS" ]; then
        if [[ ":$PATH:" != *":${BIN_DIR}:"* ]]; then
            echo ""
            echo -e "${YELLOW}⚠ Warning: ${BIN_DIR} is not in your PATH${NC}"
            echo ""
            echo "Add this to your shell config (~/.bashrc, ~/.zshrc, or ~/.profile):"
            echo -e "${GREEN}export PATH=\"${BIN_DIR}:\$PATH\"${NC}"
            echo ""
            echo "Then reload your shell:"
            echo -e "${GREEN}source ~/.bashrc${NC}  # or ~/.zshrc, ~/.profile"
            echo ""
        fi
    fi
}

# Start daemon
start_daemon() {
    echo ""
    echo "Starting daemon..."
    
    if is_daemon_running; then
        echo -e "${YELLOW}Daemon already running${NC}"
        return 0
    fi
    
    # Ensure clean state
    rm -f "${PID_FILE}" "${SOCK_FILE}"
    
    # Start the daemon
    if [ "$OS" = "Windows" ]; then
        # Windows background process
        nohup "${BIN_DIR}/${DAEMON_BIN}" > "${LOG_FILE}" 2>&1 &
        echo $! > "${PID_FILE}"
    else
        "${BIN_DIR}/${DAEMON_BIN}" > "${LOG_FILE}" 2>&1 &
        local daemon_pid=$!
        echo $daemon_pid > "${PID_FILE}"
    fi
    
    # Wait and verify it started
    local waited=0
    local max_wait=5
    while [ $waited -lt $max_wait ]; do
        sleep 1
        waited=$((waited+1))
        
        if is_daemon_running; then
            # Clean up build directory
            rm -rf bin/ 2>/dev/null || true
            echo -e "${GREEN}✓ Daemon started (logs: ${LOG_FILE})${NC}"
            return 0
        fi
    done
    
    # Failed to start
    echo -e "${RED}✗ Failed to start daemon after ${max_wait} seconds${NC}"
    echo ""
    echo "Check the logs for details:"
    echo "  cat ${LOG_FILE}"
    echo ""
    if [ -f "${LOG_FILE}" ]; then
        echo "Last 10 lines of log:"
        tail -10 "${LOG_FILE}"
    fi
    return 1
}

# Show debug information
show_debug_info() {
    echo ""
    echo -e "${GREEN}=== Debug Information ===${NC}"
    echo ""
    echo "Platform: $OS"
    echo "Binary directory: ${BIN_DIR}"
    echo "PID file: ${PID_FILE}"
    echo "Log file: ${LOG_FILE}"
    echo "Socket file: ${SOCK_FILE}"
    echo ""
    
    echo "PID file status:"
    if [ -f "${PID_FILE}" ]; then
        local pid=$(cat "${PID_FILE}" 2>/dev/null || echo "")
        echo "  Exists: YES"
        echo "  PID: $pid"
        if [ -n "$pid" ]; then
            if kill -0 "$pid" 2>/dev/null; then
                echo "  Process alive: YES"
            else
                echo "  Process alive: NO (stale PID file)"
            fi
        fi
    else
        echo "  Exists: NO"
    fi
    echo ""
    
    echo "Running processes:"
    if [ "$OS" = "Windows" ]; then
        tasklist 2>/dev/null | grep -i "mosaicd" || echo "  None found"
    else
        pgrep -fl "mosaicd" || echo "  None found"
    fi
    echo ""
    
    echo "Socket file:"
    ls -la "${SOCK_FILE}" 2>/dev/null || echo "  Does not exist"
    echo ""
    
    if [ -f "${LOG_FILE}" ]; then
        echo "Log file (last 10 lines):"
        tail -10 "${LOG_FILE}"
    else
        echo "Log file: Does not exist"
    fi
    echo ""
}

# Print success message
print_success() {
    echo ""
    echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${GREEN}✓ Mosaic is ready!${NC}"
    echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    echo "Platform: $OS"
    echo "Installed to: ${BIN_DIR}"
    echo ""
    echo "Usage:"
    echo "  mos help       - View all mosaic commands"
    echo "  mos shutdown   - Stop daemon and cleanup"
    echo ""
    
    if [ "$OS" != "macOS" ]; then
        echo "Daemon management:"
        echo "  make status    - Check daemon status"
        echo "  make stop      - Stop the daemon"
        echo "  make restart   - Restart the daemon"
        echo ""
    fi
    
    echo "Logs: ${LOG_FILE}"
    echo ""
}

# Main installation flow
main() {
    echo -e "${GREEN}Mosaic Installation Script${NC}"
    echo ""
    
    set_platform_vars
    echo "Detected OS: $OS"
    echo ""
    
    # Check for Go
    if ! command -v go &> /dev/null; then
        echo -e "${RED}✗ Go is not installed${NC}"
        echo "Please install Go from https://golang.org/dl/"
        exit 1
    fi
    
    # Stop any existing daemon
    stop_daemon || true
    
    # Build and install
    build_binaries
    install_binaries
    
    # Start daemon
    if start_daemon; then
        print_success
    else
        echo ""
        echo -e "${RED}Installation completed but daemon failed to start${NC}"
        show_debug_info
        exit 1
    fi
}

# Handle command line arguments
if [ "$1" = "--debug" ] || [ "$1" = "-d" ]; then
    set_platform_vars
    show_debug_info
    exit 0
elif [ "$1" = "--stop" ] || [ "$1" = "-s" ]; then
    set_platform_vars
    stop_daemon
    exit $?
elif [ "$1" = "--help" ] || [ "$1" = "-h" ]; then
    echo "Mosaic Installation Script"
    echo ""
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  (no options)    Install Mosaic"
    echo "  --debug, -d     Show debug information"
    echo "  --stop, -s      Stop the daemon"
    echo "  --help, -h      Show this help message"
    echo ""
    exit 0
fi

# Run main installation
main