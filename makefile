.PHONY: build install clean start stop uninstall status help quickstart shutdown restart

# Detect OS
ifeq ($(OS),Windows_NT)
    DETECTED_OS := Windows
    EXE_EXT := .exe
    BIN_DIR := $(APPDATA)/mosaic/bin
    TEMP_DIR := $(TEMP)
    INSTALL_CMD := @echo "Copying to $(BIN_DIR)..." && mkdir -p "$(BIN_DIR)" && cp
    KILL_CMD := taskkill /F /IM
    PS_CMD := tasklist | findstr
    SOCK_EXT := 
    PATH_SEP := ;
else
    DETECTED_OS := $(shell uname -s)
    EXE_EXT :=
    TEMP_DIR := /tmp
    PATH_SEP := :
    
    ifeq ($(DETECTED_OS),Linux)
        BIN_DIR := $(HOME)/.local/bin
        INSTALL_CMD := @echo "Installing to $(BIN_DIR)..." && mkdir -p $(BIN_DIR) && cp
        KILL_CMD := pkill -f
        PS_CMD := pgrep -f
        SOCK_EXT := .sock
    else ifeq ($(DETECTED_OS),Darwin)
        BIN_DIR := /usr/local/bin
        INSTALL_CMD := @echo "Installing to $(BIN_DIR)..." && sudo cp
        KILL_CMD := pkill -f
        PS_CMD := pgrep -f
        SOCK_EXT := .sock
    else
        BIN_DIR := $(HOME)/.local/bin
        INSTALL_CMD := @echo "Installing to $(BIN_DIR)..." && mkdir -p $(BIN_DIR) && cp
        KILL_CMD := pkill -f
        PS_CMD := pgrep -f
        SOCK_EXT := .sock
    endif
endif

# Binary names
CLI_BIN := mos$(EXE_EXT)
DAEMON_BIN := mosaicd$(EXE_EXT)
PID_FILE := $(TEMP_DIR)/mosaicd.pid
LOG_FILE := $(TEMP_DIR)/mosaicd.log
SOCK_FILE := $(TEMP_DIR)/mosaicd$(SOCK_EXT)

# Build binaries
build:
	@echo "Building for $(DETECTED_OS)..."
	@mkdir -p bin
	go build -o bin/$(CLI_BIN) ./cmd/mosaic-cli
	go build -o bin/$(DAEMON_BIN) ./cmd/mosaic-node
	@echo "✓ Build complete! Binaries in bin/"

# Install to system
install: build
	$(INSTALL_CMD) bin/$(CLI_BIN) $(BIN_DIR)/
	$(INSTALL_CMD) bin/$(DAEMON_BIN) $(BIN_DIR)/
	@echo "✓ Installed successfully!"
	@echo ""
	@echo "Usage:"
	@echo "  make quickstart     - Quickstarts mosaic"
	@echo "  mos help            - View all mosaic-cli commands"
	@echo "  make shutdown       - Kills mosaic process and cleans up files"
ifeq ($(DETECTED_OS),Linux)
	@echo ""
	@echo "Note: Make sure $(BIN_DIR) is in your PATH"
	@echo "Add this to your ~/.bashrc or ~/.zshrc:"
	@echo "  export PATH=\"$(BIN_DIR)$(PATH_SEP)\$$PATH\""
endif
ifeq ($(DETECTED_OS),Windows)
	@echo ""
	@echo "Note: Add $(BIN_DIR) to your PATH environment variable"
endif

# Start daemon
start:
ifeq ($(DETECTED_OS),Windows)
	@tasklist | findstr /I "mosaicd.exe" > nul && echo "Daemon already running" || ( \
		start /B mosaicd.exe > $(LOG_FILE) 2>&1 && \
		echo "✓ Daemon started (logs: $(LOG_FILE))" \
	)
	@if exist bin rmdir /S /Q bin
else
	@if $(PS_CMD) mosaicd > /dev/null 2>&1; then \
		echo "Daemon already running"; \
	else \
		$(DAEMON_BIN) > $(LOG_FILE) 2>&1 & \
		echo $$! > $(PID_FILE); \
		rm -rf bin/; \
		echo "✓ Daemon started (logs: $(LOG_FILE))"; \
	fi
endif

# Stop daemon
stop:
	@echo "Stopping daemon..."
	@# Try PID file first
	@if [ -f $(PID_FILE) ]; then \
		PID=$$(cat $(PID_FILE)); \
		if kill -0 $$PID 2>/dev/null; then \
			echo "Killing daemon (PID: $$PID)..."; \
			kill $$PID 2>/dev/null || true; \
			sleep 1; \
			if kill -0 $$PID 2>/dev/null; then \
				echo "Process still alive, using SIGKILL..."; \
				kill -9 $$PID 2>/dev/null || true; \
			fi; \
		else \
			echo "PID file exists but process not running"; \
		fi; \
		rm -f $(PID_FILE); \
	fi
	@# Fallback: kill by process name
	@if pgrep -f "mosaicd" > /dev/null 2>&1; then \
		echo "Found running mosaicd processes, killing..."; \
		pkill -f "mosaicd" 2>/dev/null || true; \
		sleep 1; \
		if pgrep -f "mosaicd" > /dev/null 2>&1; then \
			echo "Processes still alive, using SIGKILL..."; \
			pkill -9 -f "mosaicd" 2>/dev/null || true; \
		fi; \
	fi
	@# Clean up socket
	@rm -f $(SOCK_FILE)
	@# Verify everything is stopped
	@if pgrep -f "mosaicd" > /dev/null 2>&1; then \
		echo "⚠ Warning: mosaicd processes still running:"; \
		ps aux | grep mosaicd | grep -v grep; \
		echo "You may need to manually kill these processes"; \
	else \
		echo "✓ Daemon stopped"; \
	fi

# Remove built binaries
clean:
	@echo "Cleaning up..."
ifeq ($(DETECTED_OS),Windows)
	@if exist bin rmdir /S /Q bin
	@if exist $(SOCK_FILE) del /Q $(SOCK_FILE)
	@if exist $(PID_FILE) del /Q $(PID_FILE)
	@if exist $(LOG_FILE) del /Q $(LOG_FILE)
else
	rm -rf bin/
	rm -f $(SOCK_FILE) $(PID_FILE) $(LOG_FILE)
endif
	@echo "✓ Cleaned"

# Restart daemon
restart_daemon: stop start

restart: shutdown quickstart

# Uninstall from system
uninstall: stop
	@echo "Uninstalling..."
ifeq ($(DETECTED_OS),Darwin)
	sudo rm -f $(BIN_DIR)/$(CLI_BIN)
	sudo rm -f $(BIN_DIR)/$(DAEMON_BIN)
else ifeq ($(DETECTED_OS),Windows)
	@if exist $(BIN_DIR)\$(CLI_BIN) del /Q $(BIN_DIR)\$(CLI_BIN)
	@if exist $(BIN_DIR)\$(DAEMON_BIN) del /Q $(BIN_DIR)\$(DAEMON_BIN)
else
	rm -f $(BIN_DIR)/$(CLI_BIN)
	rm -f $(BIN_DIR)/$(DAEMON_BIN)
endif
	@echo "✓ Uninstalled"

# Show status
status:
	@echo "Platform: $(DETECTED_OS)"
	@echo "Install directory: $(BIN_DIR)"
	@echo ""
ifeq ($(DETECTED_OS),Windows)
	@tasklist | findstr /I "mosaicd.exe" && echo "✓ Daemon is running" || echo "✗ Daemon is not running"
else
	@if $(PS_CMD) mosaicd > /dev/null 2>&1; then \
		echo "✓ Daemon is running"; \
		ps aux | grep mosaicd | grep -v grep; \
	else \
		echo "✗ Daemon is not running"; \
	fi
	@if [ -e $(SOCK_FILE) ]; then \
		echo "✓ Socket exists: $(SOCK_FILE)"; \
	else \
		echo "✗ Socket not found"; \
	fi
endif

# Quickstart and shutdown
quickstart: build install start

shutdown: stop uninstall clean

# Show help
help:
	@echo "Mosaic Makefile Commands ($(DETECTED_OS)):"
	@echo ""
	@echo "  make build      - Build binaries to bin/"
	@echo "  make install    - Build and install to $(BIN_DIR)"
	@echo "  make start      - Start the daemon"
	@echo "  make stop       - Stop the daemon"
	@echo "  make restart    - Restart the daemon"
	@echo "  make status     - Check daemon status"
	@echo "  make clean      - Remove build artifacts"
	@echo "  make uninstall  - Remove from system"
	@echo "  make quickstart - Build, install, and start"
	@echo "  make shutdown   - Stop, uninstall, and clean"
	@echo ""
	@echo "After install, use: mos help to view all mosaic commands"