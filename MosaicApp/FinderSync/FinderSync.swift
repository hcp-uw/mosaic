import Cocoa
import FinderSync

/// Mosaic Finder Sync Extension.
///
/// Watches ~/Mosaic/ and:
///   - Sets badge icons on each file (green = cached locally, grey = remote only)
///   - Adds a right-click context menu with File Info, Delete, and Force Re-fetch
///
/// All network/peer logic stays in the Go daemon. This class is just a thin UI bridge
/// that calls DaemonClient, which in turn talks to localhost:7777.
class FinderSync: FIFinderSync {

    override init() {
        super.init()

        let mosaicDir = URL(fileURLWithPath: NSHomeDirectory()).appendingPathComponent("Mosaic")
        FIFinderSyncController.default().directoryURLs = [mosaicDir]

        // Badge images must be added as assets named "badge_synced" and "badge_remote"
        // in the Xcode asset catalog before building.
        if let synced = NSImage(named: "badge_synced") {
            FIFinderSyncController.default().setBadgeImage(synced,
                label: "On Network", forBadgeIdentifier: "synced")
        }
        if let remote = NSImage(named: "badge_remote") {
            FIFinderSyncController.default().setBadgeImage(remote,
                label: "Remote Only", forBadgeIdentifier: "remote")
        }
    }

    // Called for each file Finder displays — assign its badge.
    override func requestBadgeIdentifier(for url: URL) {
        let filename = url.lastPathComponent
        DaemonClient.shared.getFileStatus(filename) { status in
            let badge = (status?.isCached == true) ? "synced" : "remote"
            DispatchQueue.main.async {
                FIFinderSyncController.default().setBadgeIdentifier(badge, for: url)
            }
        }
    }

    // MARK: - Context menu

    override func menu(for menuKind: FIMenuKind) -> NSMenu? {
        guard menuKind == .contextualMenuForItems else { return nil }
        let menu = NSMenu(title: "Mosaic")
        menu.addItem(withTitle: "File Info",
                     action: #selector(showInfo(_:)), keyEquivalent: "")
        menu.addItem(withTitle: "Delete from Network",
                     action: #selector(deleteFile(_:)), keyEquivalent: "")
        menu.addItem(withTitle: "Force Re-fetch",
                     action: #selector(refetch(_:)), keyEquivalent: "")
        return menu
    }

    @IBAction func showInfo(_ sender: AnyObject?) {
        guard let items = FIFinderSyncController.default().selectedItemURLs() else { return }
        for item in items {
            DaemonClient.shared.getFileStatus(item.lastPathComponent) { status in
                guard let status else { return }
                DispatchQueue.main.async {
                    let alert = NSAlert()
                    alert.messageText = status.name
                    alert.informativeText = """
                        Size: \(status.size) GB
                        Node: \(status.nodeID)
                        Added: \(status.dateAdded)
                        Cached locally: \(status.isCached)
                        """
                    alert.runModal()
                }
            }
        }
    }

    @IBAction func deleteFile(_ sender: AnyObject?) {
        guard let items = FIFinderSyncController.default().selectedItemURLs() else { return }
        for item in items {
            DaemonClient.shared.delete(item.lastPathComponent) { success in
                if success {
                    try? FileManager.default.removeItem(at: item)
                }
            }
        }
    }

    @IBAction func refetch(_ sender: AnyObject?) {
        guard let items = FIFinderSyncController.default().selectedItemURLs() else { return }
        for item in items {
            DaemonClient.shared.fetch(item.lastPathComponent) { _ in }
        }
    }
}
