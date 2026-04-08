import Cocoa

class MosaicDocumentController: NSDocumentController {
    override func openDocument(withContentsOf url: URL,
                               display displayDocument: Bool,
                               completionHandler: @escaping (NSDocument?, Bool, Error?) -> Void) {
        guard url.pathExtension.lowercased() == "mosaic" else {
            super.openDocument(withContentsOf: url, display: displayDocument, completionHandler: completionHandler)
            return
        }
        completionHandler(nil, false, nil)
        (NSApp.delegate as? AppDelegate)?.handleOpen(path: url.path)
    }
}

class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem?
    private var daemonTask: Process?
    private var dirWatcher: DispatchSourceFileSystemObject?

    func applicationWillFinishLaunching(_ notification: Notification) {
        _ = MosaicDocumentController()
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        if let button = statusItem?.button {
            button.image = NSImage(systemSymbolName: "externaldrive.connected.to.line.below",
                                   accessibilityDescription: "Mosaic")
        }
        buildMenu()
        watchMosaicDirectory()
        launchDaemon()
    }

    func applicationWillTerminate(_ notification: Notification) {
        dirWatcher?.cancel()
        daemonTask?.terminate()
    }

    // Watch ~/Mosaic/ for added/removed stubs and rebuild the menu automatically.
    private func watchMosaicDirectory() {
        let mosaicDir = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Mosaic").path
        let fd = open(mosaicDir, O_EVTONLY)
        guard fd >= 0 else { return }

        let source = DispatchSource.makeFileSystemObjectSource(
            fileDescriptor: fd,
            eventMask: [.write, .delete, .rename],
            queue: .main
        )
        source.setEventHandler { [weak self] in
            self?.buildMenu()
        }
        source.setCancelHandler {
            close(fd)
        }
        source.resume()
        dirWatcher = source
    }

    // MARK: - Status bar menu

    private func buildMenu() {
        // Read stubs directly from disk — no daemon required.
        let mosaicDir = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Mosaic")
        let stubs = (try? FileManager.default.contentsOfDirectory(
            at: mosaicDir, includingPropertiesForKeys: nil))?.filter {
                $0.pathExtension == "mosaic"
            } ?? []

        let menu = NSMenu()
        menu.addItem(withTitle: "Mosaic", action: nil, keyEquivalent: "")
        menu.addItem(.separator())

        if stubs.isEmpty {
            menu.addItem(withTitle: "No files on network", action: nil, keyEquivalent: "")
        } else {
            for stub in stubs.sorted(by: { $0.lastPathComponent < $1.lastPathComponent }) {
                let filename = stub.deletingPathExtension().lastPathComponent
                let item = NSMenuItem(title: filename, action: nil, keyEquivalent: "")
                let sub = NSMenu()

                // Read metadata from stub JSON
                if let data = try? Data(contentsOf: stub),
                   let meta = try? JSONDecoder().decode(StubMeta.self, from: data) {
                    let realFileExists = FileManager.default.fileExists(
                        atPath: mosaicDir.appendingPathComponent(filename).path)
                    let cached = realFileExists ? "Yes" : "No"
                    sub.addItem(withTitle: "Size: \(Self.formatBytes(meta.size))", action: nil, keyEquivalent: "")
                    sub.addItem(withTitle: "Added: \(meta.dateAdded)", action: nil, keyEquivalent: "")
                    sub.addItem(withTitle: "Cached locally: \(cached)", action: nil, keyEquivalent: "")
                    sub.addItem(.separator())
                }

                let openItem = NSMenuItem(title: "Open", action: #selector(openFileFromMenu(_:)), keyEquivalent: "")
                openItem.representedObject = filename
                openItem.target = self
                sub.addItem(openItem)

                let refetchItem = NSMenuItem(title: "Re-fetch", action: #selector(refetchFromMenu(_:)), keyEquivalent: "")
                refetchItem.representedObject = filename
                refetchItem.target = self
                sub.addItem(refetchItem)

                item.submenu = sub
                menu.addItem(item)
            }
        }

        menu.addItem(.separator())
        menu.addItem(withTitle: "Quit Mosaic", action: #selector(quitMosaic), keyEquivalent: "q")

        statusItem?.menu = menu
    }

    // Mirrors the Go StubMeta struct for decoding stub JSON in Swift.
    private struct StubMeta: Decodable {
        let name: String
        let size: Int
        let nodeID: Int
        let dateAdded: String
        let cached: Bool
    }

    @objc private func openFileFromMenu(_ sender: NSMenuItem) {
        guard let filename = sender.representedObject as? String else { return }
        let realURL = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Mosaic")
            .appendingPathComponent(filename)
        if FileManager.default.fileExists(atPath: realURL.path) {
            openFile(realURL)
        } else {
            fetchAndOpen(filename: filename, realURL: realURL)
        }
    }

    @objc private func refetchFromMenu(_ sender: NSMenuItem) {
        guard let filename = sender.representedObject as? String else { return }
        let realURL = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Mosaic")
            .appendingPathComponent(filename)
        fetchAndOpen(filename: filename, realURL: realURL)
    }

    // MARK: - File open (called from double-clicking a stub)

    func handleOpen(path: String) {
        let url = URL(fileURLWithPath: path)
        let filename = url.deletingPathExtension().lastPathComponent
        let realURL = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Mosaic")
            .appendingPathComponent(filename)

        if FileManager.default.fileExists(atPath: realURL.path) {
            openFile(realURL)
        } else {
            fetchAndOpen(filename: filename, realURL: realURL)
        }
    }

    private func fetchAndOpen(filename: String, realURL: URL) {
        DaemonClient.shared.fetch(filename) { _ in
            DispatchQueue.main.async {
                if FileManager.default.fileExists(atPath: realURL.path) {
                    self.openFile(realURL)
                } else {
                    let alert = NSAlert()
                    alert.messageText = "Could not fetch \(filename)"
                    alert.informativeText = "The daemon could not retrieve this file from the network."
                    alert.runModal()
                }
            }
        }
    }

    private func openFile(_ url: URL) {
        let config = NSWorkspace.OpenConfiguration()
        config.activates = true
        NSWorkspace.shared.open(url, configuration: config, completionHandler: nil)
    }

    // MARK: - Helpers

    private static func formatBytes(_ bytes: Int) -> String {
        guard bytes > 0 else { return "Unknown" }
        let kb = Double(bytes) / 1024
        let mb = kb / 1024
        let gb = mb / 1024
        if gb >= 1 { return String(format: "%.1f GB", gb) }
        if mb >= 1 { return String(format: "%.1f MB", mb) }
        return String(format: "%.0f KB", kb)
    }

    @objc private func quitMosaic() {
        NSApplication.shared.terminate(nil)
    }

    // MARK: - Daemon

    private func launchDaemon() {
        let candidates: [String] = [
            Bundle.main.bundlePath + "/Contents/MacOS/mosaic-node",
            "/usr/local/bin/mosaic-node",
            "/opt/homebrew/bin/mosaic-node",
        ]
        guard let execPath = candidates.first(where: { FileManager.default.isExecutableFile(atPath: $0) }) else {
            return
        }
        let task = Process()
        task.executableURL = URL(fileURLWithPath: execPath)
        try? task.run()
        daemonTask = task
    }
}
