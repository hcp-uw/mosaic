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

# Check if the Swift menu bar app is running
is_app_running() {
    pgrep -x "Mosaic" > /dev/null 2>&1
}

# Stop the Swift menu bar app if it's running
stop_app() {
    if is_app_running; then
        echo "Stopping Mosaic app..."
        pkill -x "Mosaic" 2>/dev/null || true
        pkill -x "MosaicFinderSync" 2>/dev/null || true
        sleep 1
        if is_app_running; then
            pkill -9 -x "Mosaic" 2>/dev/null || true
            sleep 1
        fi
        echo -e "${GREEN}✓ Mosaic app stopped${NC}"
    fi
}

# Build the Swift menu bar app using xcodebuild (requires Xcode.app, not just CLT).
build_app() {
    if [ "$OS" != "macOS" ]; then
        return 0
    fi

    local script_dir
    script_dir="$(cd "$(dirname "$0")" && pwd)"
    local project="${script_dir}/MosaicApp/Mosaic.xcodeproj"

    if [ ! -d "$project" ]; then
        echo -e "${YELLOW}⚠ MosaicApp/Mosaic.xcodeproj not found — skipping app build${NC}"
        return 0
    fi

    if ! command -v xcodebuild &> /dev/null; then
        echo -e "${YELLOW}⚠ xcodebuild not found — skipping app build (install Xcode.app)${NC}"
        return 0
    fi

    echo ""
    echo "Building Mosaic.app..."

    local derived="${script_dir}/MosaicApp/DerivedData"

    xcodebuild \
        -project "$project" \
        -scheme Mosaic \
        -configuration Release \
        -derivedDataPath "$derived" \
        CODE_SIGN_IDENTITY="-" \
        DEVELOPMENT_TEAM="" \
        CODE_SIGNING_ALLOWED=YES \
        > /tmp/mosaic-xcodebuild.log 2>&1

    if [ $? -ne 0 ]; then
        echo -e "${YELLOW}⚠ App build failed — check /tmp/mosaic-xcodebuild.log${NC}"
        echo "  The CLI and daemon will work normally without the menu bar app."
        return 0
    fi

    local app_path="${derived}/Build/Products/Release/Mosaic.app"
    if [ -d "$app_path" ]; then
        # Remove quarantine so Gatekeeper doesn't block it on first launch.
        xattr -dr com.apple.quarantine "$app_path" 2>/dev/null || true
        echo -e "${GREEN}✓ Mosaic.app built${NC}"
    fi
}

# Launch the Swift menu bar app.
# Uses the bundle ID so macOS finds it wherever it's registered — the same
# mechanism that auto-launches it when a .mosaic file is double-clicked.
# Falls back to searching known paths, then warns and continues if not found.
start_app() {
    if [ "$OS" != "macOS" ]; then
        return 0
    fi

    echo ""
    echo "Starting Mosaic app..."

    # Try bundle ID first — works as long as the app has been run at least once.
    if open -b "com.mosaic.Mosaic" 2>/dev/null; then
        sleep 2
        if is_app_running; then
            echo -e "${GREEN}✓ Mosaic app started${NC}"
            return 0
        fi
    fi

    # Fall back to known paths.
    local script_dir
    script_dir="$(cd "$(dirname "$0")" && pwd)"
    local candidates=(
        "/Applications/Mosaic.app"
        "${HOME}/Applications/Mosaic.app"
        "${script_dir}/MosaicApp/build/Release/Mosaic.app"
        "${script_dir}/MosaicApp/DerivedData/Build/Products/Release/Mosaic.app"
    )

    for candidate in "${candidates[@]}"; do
        if [ -d "$candidate" ]; then
            open "$candidate" 2>/dev/null
            sleep 2
            if is_app_running; then
                echo -e "${GREEN}✓ Mosaic app started from ${candidate}${NC}"
                return 0
            fi
        fi
    done

    echo -e "${YELLOW}⚠ Mosaic.app not found — running without the menu bar app.${NC}"
    echo "  Build and run the app in Xcode at least once to register it:"
    echo "  open MosaicApp/Mosaic.xcodeproj"
    echo "  The CLI and daemon will work normally without it."
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
                # Wait up to 4s for graceful disconnect (daemon leaves P2P network on SIGTERM).
                local wait=0
                while [ $wait -lt 4 ] && kill -0 "$pid" 2>/dev/null; do
                    sleep 1
                    wait=$((wait+1))
                done

                # Force kill if still alive.
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

    # When running as root (sudo), Go may not be in PATH.
    # Run the build as the real user so their PATH and Go installation are used.
    local build_cmd
    if [ "$OS" = "Windows" ]; then
        build_cmd="GOOS=windows GOARCH=amd64 go build -o bin/${CLI_BIN} ./cmd/mosaic-cli && GOOS=windows GOARCH=amd64 go build -o bin/${DAEMON_BIN} ./cmd/mosaic-node"
    else
        build_cmd="go build -o bin/${CLI_BIN} ./cmd/mosaic-cli && go build -o bin/${DAEMON_BIN} ./cmd/mosaic-node"
    fi

    if [ -n "$SUDO_USER" ]; then
        chown "$SUDO_USER" bin
        su "$SUDO_USER" -c "cd $(pwd) && $build_cmd"
    else
        eval "$build_cmd"
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
    
    # Ensure clean state — remove files that may be owned by root from a previous sudo run.
    rm -f "${PID_FILE}" "${SOCK_FILE}" "${LOG_FILE}"

    # Start the daemon as the real user (not root), even if install.sh was run with sudo.
    if [ "$OS" = "Windows" ]; then
        # Windows background process
        nohup "${BIN_DIR}/${DAEMON_BIN}" > "${LOG_FILE}" 2>&1 &
        echo $! > "${PID_FILE}"
    else
        if [ -n "$SUDO_USER" ]; then
            # install.sh was run with sudo — drop back to the real user for the daemon.
            # Touch the log file as root first so su can write to it, then hand ownership over.
            touch "${LOG_FILE}"
            chown "$SUDO_USER" "${LOG_FILE}"
            local daemon_pid
            daemon_pid=$(su "$SUDO_USER" -c "${BIN_DIR}/${DAEMON_BIN} >> ${LOG_FILE} 2>&1 & echo \$!")
            echo "$daemon_pid" > "${PID_FILE}"
        else
            "${BIN_DIR}/${DAEMON_BIN}" > "${LOG_FILE}" 2>&1 &
            local daemon_pid=$!
            echo $daemon_pid > "${PID_FILE}"
        fi
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

# Check if the user is already logged in by reading the session file
is_logged_in() {
    local real_home
    if [ -n "$SUDO_USER" ]; then
        real_home=$(eval echo "~$SUDO_USER")
    else
        real_home="$HOME"
    fi
    [ -f "${real_home}/.mosaic-session" ]
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

    if is_logged_in; then
        echo -e "${GREEN}✓ Logged in${NC}"
        echo ""
        echo "Next steps:"
        echo "  mos join <stun-server-ip>:3478   - Connect to the network"
        echo "  mos upload file <path>            - Upload a file"
        echo "  mos download file <name>          - Download a file"
    else
        echo -e "${YELLOW}⚠ You are not logged in.${NC}"
        echo ""
        echo "To get started:"
        echo -e "  ${GREEN}mos login <key>${NC}   - Log in with your key"
        echo ""
        echo "Then connect to the network:"
        echo "  mos join <stun-server-ip>:3478"
    fi

    echo ""
    echo "  mos help            - View all commands"
    echo "  mos shutdown        - Stop daemon and cleanup"
    echo "  ./install.sh --stop - Stop daemon and menu bar app"
    echo ""
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
    
    # Stop any existing processes
    stop_app
    stop_daemon || true

    # Build and install
    build_binaries
    install_binaries

    # Start daemon
    if ! start_daemon; then
        echo ""
        echo -e "${RED}Installation completed but daemon failed to start${NC}"
        show_debug_info
        exit 1
    fi

    # Build and start the Swift app (best-effort — warns but doesn't fail)
    build_app
    start_app

    print_success
}

# Handle command line arguments
if [ "$1" = "--debug" ] || [ "$1" = "-d" ]; then
    set_platform_vars
    show_debug_info
    exit 0
elif [ "$1" = "--stop" ] || [ "$1" = "-s" ]; then
    set_platform_vars
    stop_app
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