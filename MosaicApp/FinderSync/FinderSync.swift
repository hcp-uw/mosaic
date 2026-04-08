import Cocoa
import FinderSync

class FinderSync: FIFinderSync {

    override init() {
        super.init()
        let mosaicDir = URL(fileURLWithPath: NSHomeDirectory()).appendingPathComponent("Mosaic")
        FIFinderSyncController.default().directoryURLs = [mosaicDir]

        if let synced = NSImage(named: "badge_synced") {
            FIFinderSyncController.default().setBadgeImage(synced,
                label: "On Network", forBadgeIdentifier: "synced")
        }
        if let remote = NSImage(named: "badge_remote") {
            FIFinderSyncController.default().setBadgeImage(remote,
                label: "Remote Only", forBadgeIdentifier: "remote")
        }
    }

    override func requestBadgeIdentifier(for url: URL) {
        let filename = url.lastPathComponent
        DaemonClient.shared.getFileStatus(filename) { status in
            let badge = (status?.isCached == true) ? "synced" : "remote"
            DispatchQueue.main.async {
                FIFinderSyncController.default().setBadgeIdentifier(badge, for: url)
            }
        }
    }
}
