# Handle command line arguments
param(
    [switch]$Debug,
    [switch]$Stop,
    [switch]$Help
)

# Mosaic Installation Script for Windows
# Requires PowerShell 5.1 or higher

# Enable strict mode
Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# Set console to UTF-8
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

# Colors for output
function Write-ColorOutput($ForegroundColor) {
    $fc = $host.UI.RawUI.ForegroundColor
    $host.UI.RawUI.ForegroundColor = $ForegroundColor
    if ($args) {
        Write-Output $args
    }
    $host.UI.RawUI.ForegroundColor = $fc
}

function Write-Success { Write-ColorOutput Green $args }
function Write-Error { Write-ColorOutput Red $args }
function Write-Warning { Write-ColorOutput Yellow $args }
function Write-Info { Write-ColorOutput Cyan $args }

# Find Go executable
function Find-Go {
    # Try direct command first
    $goCmd = Get-Command go -ErrorAction SilentlyContinue
    if ($goCmd) {
        return "go"
    }
    
    # Common Go installation paths
    $possiblePaths = @(
        "C:\Program Files\Go\bin\go.exe",
        "C:\Go\bin\go.exe",
        "$env:USERPROFILE\go\bin\go.exe",
        "$env:LOCALAPPDATA\Programs\Go\bin\go.exe"
    )
    
    foreach ($path in $possiblePaths) {
        if (Test-Path $path) {
            return $path
        }
    }
    
    # Try to find in registry
    try {
        $goPath = (Get-ItemProperty "HKLM:\SOFTWARE\Golang").InstallPath
        if ($goPath -and (Test-Path "$goPath\bin\go.exe")) {
            return "$goPath\bin\go.exe"
        }
    } catch {
        # Registry key doesn't exist
    }
    
    return $null
}

# Set platform-specific variables
$BIN_DIR = Join-Path $env:APPDATA "mosaic\bin"
$TEMP_DIR = $env:TEMP
$CLI_BIN = "mos.exe"
$DAEMON_BIN = "mosaicd.exe"
$PID_FILE = Join-Path $TEMP_DIR "mosaicd.pid"
$LOG_FILE = Join-Path $TEMP_DIR "mosaicd.log"
$SOCK_FILE = Join-Path $TEMP_DIR "mosaicd.sock"
$PORT_FILE = Join-Path $TEMP_DIR "mosaicd.port"

# Check if daemon is running
function Test-DaemonRunning {
    $process = Get-Process -Name "mosaicd" -ErrorAction SilentlyContinue
    return $null -ne $process
}

# Stop daemon
function Stop-Daemon {
    Write-Output ""
    Write-Output "Checking for running daemons..."
    
    $stopped = $false
    $attempts = 0
    $maxAttempts = 3
    
    while ($attempts -lt $maxAttempts) {
        # Try PID file first
        if (Test-Path $PID_FILE) {
            $processId = Get-Content $PID_FILE -ErrorAction SilentlyContinue
            if ($processId) {
                try {
                    $process = Get-Process -Id $processId -ErrorAction SilentlyContinue
                    if ($process) {
                        Write-Output "Stopping daemon (PID: $processId)..."
                        Stop-Process -Id $processId -Force -ErrorAction SilentlyContinue
                        Start-Sleep -Seconds 1
                        $stopped = $true
                    }
                } catch {
                    # Process not found
                }
            }
            Remove-Item $PID_FILE -Force -ErrorAction SilentlyContinue
        }
        
        # Fallback: kill by process name
        if (Test-DaemonRunning) {
            Write-Output "Found running mosaicd processes (attempt $($attempts + 1)/$maxAttempts)..."
            Stop-Process -Name "mosaicd" -Force -ErrorAction SilentlyContinue
            Start-Sleep -Seconds 2
            $stopped = $true
        } else {
            # No processes found, we're done
            break
        }
        
        $attempts++
        
        # Check if we succeeded
        if (-not (Test-DaemonRunning)) {
            break
        }
        
        if ($attempts -lt $maxAttempts) {
            Write-Output "Retrying..."
            Start-Sleep -Seconds 1
        }
    }
    
    # Clean up socket and log
    if (Test-Path $SOCK_FILE) {
        Remove-Item $SOCK_FILE -Force -ErrorAction SilentlyContinue
    }
    # Clean up port file as well (Windows daemon may use a port file)
    if (Test-Path $PORT_FILE) {
        Remove-Item $PORT_FILE -Force -ErrorAction SilentlyContinue
    }
    if (Test-Path $LOG_FILE) {
        Remove-Item $LOG_FILE -Force -ErrorAction SilentlyContinue
    }
    
    # Final verification
    if (Test-DaemonRunning) {
        Write-Error "Warning: mosaicd processes still running after $maxAttempts attempts"
        Write-Output ""
        Write-Output "Running processes:"
        Get-Process -Name "mosaicd" -ErrorAction SilentlyContinue | Format-Table
        Write-Output ""
        Write-Warning "You may need to manually kill these processes:"
        Write-Output "  taskkill /F /IM mosaicd.exe"
        return $false
    } else {
        if ($stopped) {
            Write-Success "Daemon stopped"
        } else {
            Write-Output "No daemon was running"
        }
        return $true
    }
}

# Build binaries
function Build-Binaries {
    param([string]$GoPath)
    
    Write-Output "Building binaries for Windows..."
    
    if (-not (Test-Path "bin")) {
        New-Item -ItemType Directory -Path "bin" | Out-Null
    }
    
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    
    & $GoPath build -o "bin\$CLI_BIN" .\cmd\mosaic-cli
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to build CLI binary"
    }
    
    & $GoPath build -ldflags "-H=windowsgui" -o "bin\$DAEMON_BIN" .\cmd\mosaic-node

    if ($LASTEXITCODE -ne 0) {
        throw "Failed to build daemon binary"
    }
    
    Write-Success "Build complete!"
}

# Install binaries
function Install-Binaries {
    Write-Output ""
    Write-Output "Installing to $BIN_DIR..."
    
    # Create bin directory if it doesn't exist
    if (-not (Test-Path $BIN_DIR)) {
        New-Item -ItemType Directory -Path $BIN_DIR -Force | Out-Null
    }
    
    Copy-Item "bin\$CLI_BIN" -Destination $BIN_DIR -Force
    Copy-Item "bin\$DAEMON_BIN" -Destination $BIN_DIR -Force
    
    Write-Success "Installed successfully!"
    
    # Check if BIN_DIR is in PATH
    $currentPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($currentPath -notlike "*$BIN_DIR*") {
        Write-Output ""
        Write-Warning "Warning: $BIN_DIR is not in your PATH"
        Write-Output ""
        Write-Output "Adding to PATH automatically..."
        
        $newPath = "$BIN_DIR;$currentPath"
        [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
        $env:Path = "$BIN_DIR;$env:Path"
        
        Write-Success "Added to PATH (restart your terminal for it to take effect)"
        Write-Output ""
    }
}

# Start daemon
function Start-Daemon {
    Write-Output ""
    Write-Output "Starting daemon..."

    if (Test-DaemonRunning) {
        Write-Warning "Daemon already running"
        return $true
    }

    if (Test-Path $PID_FILE) {
        Remove-Item $PID_FILE -Force -ErrorAction SilentlyContinue
    }

    if (Test-Path $PORT_FILE) {
        Remove-Item $PORT_FILE -Force -ErrorAction SilentlyContinue
    }

    # Ensure log file exists immediately
    New-Item -ItemType File -Path $LOG_FILE -Force | Out-Null

    $daemonPath = Join-Path $BIN_DIR $DAEMON_BIN

    if (-not (Test-Path $daemonPath)) {
        Write-Error "Daemon binary not found at $daemonPath"
        return $false
    }

    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $daemonPath
    $psi.WorkingDirectory = $BIN_DIR
    $psi.UseShellExecute = $false
    $psi.CreateNoWindow = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError  = $true
    $psi.StandardOutputEncoding = [System.Text.Encoding]::UTF8
    $psi.StandardErrorEncoding  = [System.Text.Encoding]::UTF8

    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $psi

    if (-not $process.Start()) {
        Write-Error "Failed to start daemon"
        return $false
    }

    $process.Id | Out-File -FilePath $PID_FILE -Encoding ASCII

    Register-ObjectEvent $process OutputDataReceived -Action {
        if ($Event.SourceEventArgs.Data) {
            $Event.SourceEventArgs.Data | Out-File -Append $using:LOG_FILE -Encoding UTF8
        }
    } | Out-Null

    Register-ObjectEvent $process ErrorDataReceived -Action {
        if ($Event.SourceEventArgs.Data) {
            $Event.SourceEventArgs.Data | Out-File -Append $using:LOG_FILE -Encoding UTF8
        }
    } | Out-Null

    $process.BeginOutputReadLine()
    $process.BeginErrorReadLine()

    $waited = 0
    $maxWait = 5

    while ($waited -lt $maxWait) {
        Start-Sleep -Seconds 1
        $waited++

        if (Test-DaemonRunning -or (Test-Path $PORT_FILE)) {
            Write-Success "Daemon started (logs: $LOG_FILE)"
            return $true
        }
    }

    Write-Error "Failed to start daemon after $maxWait seconds"

    if (Test-Path $LOG_FILE) {
        Write-Output ""
        Write-Output "Last 10 log lines:"
        Get-Content $LOG_FILE -Tail 10
    }

    return $false
}



# Show debug information
function Show-DebugInfo {
    Write-Output ""
    Write-Success "=== Debug Information ==="
    Write-Output ""
    Write-Output "Platform: Windows"
    Write-Output "Binary directory: $BIN_DIR"
    Write-Output "PID file: $PID_FILE"
    Write-Output "Log file: $LOG_FILE"
    Write-Output "Socket file: $SOCK_FILE"
    Write-Output ""
    
    Write-Output "PID file status:"
    if (Test-Path $PID_FILE) {
        $processId = Get-Content $PID_FILE -ErrorAction SilentlyContinue
        Write-Output "  Exists: YES"
        Write-Output "  PID: $processId"
        if ($processId) {
            $process = Get-Process -Id $processId -ErrorAction SilentlyContinue
            if ($process) {
                Write-Output "  Process alive: YES"
            } else {
                Write-Output "  Process alive: NO (stale PID file)"
            }
        }
    } else {
        Write-Output "  Exists: NO"
    }
    Write-Output ""
    
    Write-Output "Running processes:"
    $processes = Get-Process -Name "mosaicd" -ErrorAction SilentlyContinue
    if ($processes) {
        $processes | Format-Table Id, ProcessName, StartTime
    } else {
        Write-Output "  None found"
    }
    Write-Output ""
    
    Write-Output "Socket file:"
    if (Test-Path $SOCK_FILE) {
        Get-Item $SOCK_FILE | Format-List
    } else {
        Write-Output "  Does not exist"
    }
    Write-Output ""
    Write-Output "Port file:"
    if (Test-Path $PORT_FILE) {
        try {
            $portContents = Get-Content $PORT_FILE -ErrorAction Stop
            Write-Output "  Exists: YES"
            Write-Output "  Path: $PORT_FILE"
            Write-Output "  Port/Address: $portContents"
        } catch {
            Write-Output "  Exists: YES (could not read contents)"
            Get-Item $PORT_FILE | Format-List
        }
    } else {
        Write-Output "  Does not exist"
    }
    Write-Output ""
    
    if (Test-Path $LOG_FILE) {
        Write-Output "Log file (last 10 lines):"
        Get-Content $LOG_FILE -Tail 10
    } else {
        Write-Output "Log file: Does not exist"
    }
    Write-Output ""
}

# Print success message
function Show-Success {
    Write-Output ""
    Write-Success "========================================"
    Write-Success "Mosaic is ready!"
    Write-Success "========================================"
    Write-Output ""
    Write-Output "Platform: Windows"
    Write-Output "Installed to: $BIN_DIR"
    Write-Output ""
    Write-Output "Usage:"
    Write-Output "  mos help       - View all mosaic commands"
    Write-Output "  mos shutdown   - Stop daemon and cleanup"
    Write-Output ""
    Write-Output "Daemon management:"
    Write-Output "  .\install.ps1 -Debug    - Check daemon status"
    Write-Output "  .\install.ps1 -Stop     - Stop the daemon"
    Write-Output ""
    Write-Output "Logs: $LOG_FILE"
    Write-Output ""
}

# Main installation flow
function Install-Mosaic {
    Write-Success "Mosaic Installation Script"
    Write-Output ""
    Write-Output "Detected OS: Windows"
    Write-Output ""
    
    # Find Go
    $goPath = Find-Go
    
    if (-not $goPath) {
        Write-Error "Go is not installed or not found"
        Write-Output "Please install Go from https://golang.org/dl/"
        exit 1
    }
    
    # Stop any existing daemon
    Stop-Daemon | Out-Null
    
    # Build and install
    try {
        Build-Binaries -GoPath $goPath
        Install-Binaries
        
        # Start daemon
        if (Start-Daemon) {
            Show-Success
        } else {
            Write-Output ""
            Write-Error "Installation completed but daemon failed to start"
            Show-DebugInfo
            exit 1
        }
    } catch {
        Write-Error "Installation failed: $_"
        exit 1
    }
}

# Process command line arguments
if ($Help) {
    Write-Output "Mosaic Installation Script for Windows"
    Write-Output ""
    Write-Output "Usage: .\install.ps1 [OPTIONS]"
    Write-Output ""
    Write-Output "Options:"
    Write-Output "  (no options)    Install Mosaic"
    Write-Output "  -Debug          Show debug information"
    Write-Output "  -Stop           Stop the daemon"
    Write-Output "  -Help           Show this help message"
    Write-Output ""
    exit 0
}

if ($Debug) {
    Show-DebugInfo
    exit 0
}

if ($Stop) {
    if (Stop-Daemon) {
        exit 0
    } else {
        exit 1
    }
}

# Run main installation
Install-Mosaic