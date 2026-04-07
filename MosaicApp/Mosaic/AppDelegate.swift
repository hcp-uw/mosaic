import Cocoa

class MosaicDocumentController: NSDocumentController {
    override func openDocument(withContentsOf url: URL,
                               display displayDocument: Bool,
                               completionHandler: @escaping (NSDocument?, Bool, Error?) -> Void) {
        NSLog("🔴 [Mosaic] MosaicDocumentController.openDocument: %@", url.path)
        let marker = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Mosaic/DEBUG_opendoc_\(Date().timeIntervalSince1970).txt")
        try? url.path.write(to: marker, atomically: true, encoding: .utf8)
        guard url.pathExtension.lowercased() == "mosaic" else {
            super.openDocument(withContentsOf: url, display: displayDocument, completionHandler: completionHandler)
            return
        }
        completionHandler(nil, false, nil)
        (NSApp.delegate as? AppDelegate)?.handleOpen(path: url.path)
    }
}

@main
class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem?
    private var daemonTask: Process?

    // This runs before ANYTHING else in the app lifecycle.
    override init() {
        super.init()
        NSLog("🔴 [Mosaic] AppDelegate.init() called — binary is new code")
        let marker = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Mosaic/DEBUG_init_\(Date().timeIntervalSince1970).txt")
        try? "AppDelegate init ran".write(to: marker, atomically: true, encoding: .utf8)
    }

    func applicationWillFinishLaunching(_ notification: Notification) {
        NSLog("🔴 [Mosaic] applicationWillFinishLaunching")
        _ = MosaicDocumentController()
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSLog("🔴 [Mosaic] applicationDidFinishLaunching")

        let marker2 = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Mosaic/DEBUG_launched_\(Date().timeIntervalSince1970).txt")
        try? "app launched".write(to: marker2, atomically: true, encoding: .utf8)

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

    func handleOpen(path: String) {
        NSLog("🔴 [Mosaic] handleOpen: %@", path)
        let url = URL(fileURLWithPath: path)
        let filename = url.deletingPathExtension().lastPathComponent
        let realURL = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Mosaic")
            .appendingPathComponent(filename)

        if FileManager.default.fileExists(atPath: realURL.path) {
            NSLog("🔴 [Mosaic] cached, opening: %@", realURL.path)
            openFile(realURL)
            return
        }

        NSLog("🔴 [Mosaic] not cached, fetching: %@", filename)
        DaemonClient.shared.fetch(filename) { success in
            NSLog("🔴 [Mosaic] fetch returned success=%d", success ? 1 : 0)
            DispatchQueue.main.async {
                if FileManager.default.fileExists(atPath: realURL.path) {
                    NSLog("🔴 [Mosaic] file exists after fetch, opening")
                    self.openFile(realURL)
                } else {
                    NSLog("🔴 [Mosaic] file missing after fetch")
                    let alert = NSAlert()
                    alert.messageText = "Could not fetch \(filename)"
                    alert.informativeText = "The daemon could not retrieve this file from the network."
                    alert.runModal()
                }
            }
        }
    }

    private func openFile(_ url: URL) {
        NSLog("🔴 [Mosaic] opening: %@", url.path)
        let config = NSWorkspace.OpenConfiguration()
        config.activates = true
        NSWorkspace.shared.open(url, configuration: config) { _, error in
            if let error {
                NSLog("🔴 [Mosaic] open failed: %@", error.localizedDescription)
            } else {
                NSLog("🔴 [Mosaic] open succeeded")
            }
        }
    }

    @objc private func quitMosaic() {
        NSApplication.shared.terminate(nil)
    }

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
