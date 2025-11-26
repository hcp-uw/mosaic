.PHONY: build install clean start stop uninstall

# Build binaries
build:
	@echo "Building binaries..."
	@mkdir -p bin
	go build -o bin/mos ./cmd/mosaic-cli
	go build -o bin/mosaicd ./cmd/mosaic-node
	@echo "Build complete! Binaries in bin/"

# Install to system
install: build
	@echo "Installing to /usr/local/bin..."
	sudo cp bin/mos /usr/local/bin/
	sudo cp bin/mosaicd /usr/local/bin/
	@echo "✓ Installed successfully!"
	@echo ""
	@echo "Usage:"
	@echo "  make quickstart     - Quickstarts mosaic"
	@echo "  mos help.           - View all mosaic-cli commands"
	@echo "  make shutdown       - Kills mosaic process and cleans up files"

# Remove built binaries
clean:
	@echo "Cleaning up..."
	rm -rf bin/
	rm -f /tmp/mosaicd.sock /tmp/mosaicd.pid /tmp/mosaicd.log
	@echo "✓ Cleaned"

# Start daemon
start:
	@if pgrep -f mosaicd > /dev/null; then \
		echo "Daemon already running"; \
	else \
		mosaicd > /tmp/mosaicd.log 2>&1 & \
		echo $$! > /tmp/mosaicd.pid; \
		sleep 1; \
		echo "✓ Daemon started (logs: /tmp/mosaicd.log)"; \
	fi

# Stop daemon
stop:
	@if [ -f /tmp/mosaicd.pid ]; then \
		kill $$(cat /tmp/mosaicd.pid) 2>/dev/null || true; \
		rm -f /tmp/mosaicd.pid /tmp/mosaicd.sock; \
		echo "✓ Daemon stopped"; \
	else \
		pkill -f mosaicd 2>/dev/null || true; \
		rm -f /tmp/mosaicd.sock; \
		echo "✓ Daemon stopped"; \
	fi

# Restart daemon
restart_daemon: stop start

restart: shutdown quickstart

# Uninstall from system
uninstall: stop
	@echo "Uninstalling..."
	sudo rm -f /usr/local/bin/mos
	sudo rm -f /usr/local/bin/mosaicd
	@echo "✓ Uninstalled"

# Show status
status:
	@if pgrep -f mosaicd > /dev/null; then \
		echo "✓ Daemon is running"; \
		ps aux | grep mosaicd | grep -v grep; \
	else \
		echo "✗ Daemon is not running"; \
	fi
	@if [ -e /tmp/mosaicd.sock ]; then \
		echo "✓ Socket exists: /tmp/mosaicd.sock"; \
	else \
		echo "✗ Socket not found"; \
	fi
quickstart: build install start
shutdown: stop uninstall clean
# Show help
help:
	@echo "Mosaic Makefile Commands:"
	@echo ""
	@echo "  make build      - Build binaries to bin/"
	@echo "  make install    - Build and install to /usr/local/bin"
	@echo "  make start      - Start the daemon"
	@echo "  make stop       - Stop the daemon"
	@echo "  make restart    - Restart the daemon"
	@echo "  make status     - Check daemon status"
	@echo "  make clean      - Remove build artifacts"
	@echo "  make uninstall  - Remove from system"
	@echo ""
	@echo "After install, use: mos upload file <path>"