import Cocoa

@main
class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem?
    private var daemonTask: Process?

    func applicationDidFinishLaunching(_ notification: Notification) {
        // No dock icon — LSUIElement = YES in Info.plist.
        // Show a minimal status bar item so users can see Mosaic is running.
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        if let button = statusItem?.button {
            button.image = NSImage(systemSymbolName: "externaldrive.connected.to.line.below",
                                   accessibilityDescription: "Mosaic")
        }

        let menu = NSMenu()
        menu.addItem(withTitle: "Mosaic is running", action: nil, keyEquivalent: "")
        menu.addItem(.separator())
        menu.addItem(withTitle: "Quit Mosaic", action: #selector(quitMosaic), keyEquivalent: "q")
        statusItem?.menu = menu

        launchDaemon()
    }

    func applicationWillTerminate(_ notification: Notification) {
        daemonTask?.terminate()
    }

    // Called when the user double-clicks a .mosaic stub file in Finder.
    func application(_ application: NSApplication, open urls: [URL]) {
        for url in urls {
            // Strip .mosaic to get the real filename, e.g. "notes.md.mosaic" → "notes.md"
            let filename = url.deletingPathExtension().lastPathComponent
            let realURL = FileManager.default.homeDirectoryForCurrentUser
                .appendingPathComponent("Mosaic")
                .appendingPathComponent(filename)

            // If already cached on disk, open immediately with its default app.
            if FileManager.default.fileExists(atPath: realURL.path) {
                NSWorkspace.shared.open(realURL)
                continue
            }

            // Otherwise fetch from the network first, then open.
            DaemonClient.shared.fetch(filename) { success in
                DispatchQueue.main.async {
                    if FileManager.default.fileExists(atPath: realURL.path) {
                        // NSWorkspace picks the default app based on the file extension
                        // (.md → Xcode/Typora, .txt → TextEdit, etc.)
                        NSWorkspace.shared.open(realURL)
                    } else {
                        let alert = NSAlert()
                        alert.messageText = "Could not fetch \(filename)"
                        alert.informativeText = "The daemon could not retrieve this file from the network."
                        alert.runModal()
                    }
                }
            }
        }
    }

    @objc private func quitMosaic() {
        NSApplication.shared.terminate(nil)
    }

    /// Launches the Go daemon if it isn't already running.
    /// Looks for mosaic-node in the app bundle first, then /usr/local/bin.
    private func launchDaemon() {
        let candidates: [String] = [
            Bundle.main.bundlePath + "/Contents/MacOS/mosaic-node",
            "/usr/local/bin/mosaic-node",
            "/opt/homebrew/bin/mosaic-node",
        ]
        guard let execPath = candidates.first(where: { FileManager.default.isExecutableFile(atPath: $0) }) else {
            showError("mosaic-node not found. Please install Mosaic.")
            return
        }

        let task = Process()
        task.executableURL = URL(fileURLWithPath: execPath)
        do {
            try task.run()
            daemonTask = task
        } catch {
            showError("Failed to start mosaic-node: \(error.localizedDescription)")
        }
    }

    private func showError(_ message: String) {
        let alert = NSAlert()
        alert.messageText = "Mosaic Error"
        alert.informativeText = message
        alert.alertStyle = .warning
        alert.runModal()
    }
}
