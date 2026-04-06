macOS: Finder Sync Extension Plan
Architecture Overview

┌─────────────────────────────────────┐
│         Mosaic.app (Swift)          │
│  ┌──────────────────────────────┐   │
│  │  FinderSyncExtension (.appex)│   │
│  │  - watches ~/Mosaic/         │   │
│  │  - sets file badges          │   │
│  │  - provides context menus    │   │
│  └──────────┬───────────────────┘   │
└─────────────┼───────────────────────┘
              │ HTTP / Unix socket
              ▼
┌─────────────────────────────────────┐
│         Go Daemon (existing)        │
│  - all network/peer logic           │
│  - file fetch, delete, info         │
│  - exposes local API                │
└─────────────────────────────────────┘
The Swift side stays thin — it's just a UI bridge. All real logic stays in Go.

Step 1: Go Daemon — Add a Local API
Your daemon needs to expose endpoints the extension can call. A Unix socket or localhost HTTP server works. You'd add routes like:


GET  /files              → list all files + sync status
GET  /files/{name}/info  → metadata, peers holding it, size, etc.
DELETE /files/{name}     → delete from network
POST /files/{name}/fetch → trigger download
You may already have some of this — it's just exposing it over a socket instead of only via CLI flags.

Step 2: Xcode Project Structure
Create a new Xcode project with two targets:

Target 1: Mosaic.app (container app)

Minimal — could just be a menu bar app (no dock icon)
Its job is to launch the Go daemon on login and host the extension
Needs com.apple.security.app-sandbox entitlement + the Finder Sync entitlement
Target 2: MosaicFinderSync.appex (the extension)

NSExtensionPrincipalClass → your FinderSync class
This is what Finder actually loads
The extension must live inside the .app bundle: Mosaic.app/Contents/PlugIns/MosaicFinderSync.appex

Step 3: The FinderSync Class (Swift)

import Cocoa
import FinderSync

class FinderSync: FIFinderSync {

    override init() {
        super.init()
        // Tell Finder which folder to watch
        let mosaicDir = URL(fileURLWithPath: NSHomeDirectory()).appendingPathComponent("Mosaic")
        FIFinderSyncController.default().directoryURLs = [mosaicDir]
        
        // Register badge images (add these as assets in Xcode)
        FIFinderSyncController.default().setBadgeImage(NSImage(named: "badge_synced")!,
            label: "On Network", forBadgeIdentifier: "synced")
        FIFinderSyncController.default().setBadgeImage(NSImage(named: "badge_remote")!,
            label: "Remote Only", forBadgeIdentifier: "remote")
    }

    // Called for each file Finder displays — set its badge
    override func requestBadgeIdentifier(for url: URL) {
        let filename = url.lastPathComponent
        // Ask your daemon: is this file cached locally or remote-only?
        DaemonClient.shared.getFileStatus(filename) { status in
            let badge = status.isCached ? "synced" : "remote"
            FIFinderSyncController.default().setBadgeIdentifier(badge, for: url)
        }
    }

    // Build the right-click context menu
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
            DaemonClient.shared.getInfo(item.lastPathComponent) { info in
                // Show an NSPanel or popover with the info
            }
        }
    }

    @IBAction func deleteFile(_ sender: AnyObject?) {
        guard let items = FIFinderSyncController.default().selectedItemURLs() else { return }
        for item in items {
            DaemonClient.shared.delete(item.lastPathComponent) { success in
                if success { try? FileManager.default.removeItem(at: item) }
            }
        }
    }
}
Step 4: DaemonClient (Swift HTTP wrapper)
Just a thin wrapper around URLSession that talks to your Go daemon:


class DaemonClient {
    static let shared = DaemonClient()
    let base = URL(string: "http://localhost:7777")! // or Unix socket

    func delete(_ filename: String, completion: @escaping (Bool) -> Void) {
        var req = URLRequest(url: base.appendingPathComponent("files/\(filename)"))
        req.httpMethod = "DELETE"
        URLSession.shared.dataTask(with: req) { _, resp, _ in
            completion((resp as? HTTPURLResponse)?.statusCode == 200)
        }.resume()
    }
    // getInfo, getFileStatus, etc. follow the same pattern
}
Step 5: Stub Files
The files in ~/Mosaic/ can be one of two things:

Tiny stubs (e.g., a 1KB .mosaic file containing JSON metadata) — Finder shows them, extension handles open/badge/menu. When user double-clicks, your registered handler fetches and opens the real file.
Real cached files — downloaded on demand, full bytes on disk. Extension just handles badge (remote vs local) and menu actions.
Which you choose depends on whether you want to save disk space for large files.

Step 6: Code Signing & Distribution
You need an Apple Developer account ($99/year)
For internal/dev use: enable the extension via System Preferences > Extensions > Finder Extensions
For distribution: notarize the app, distribute outside the App Store or through it
The extension needs the com.apple.FinderSync entitlement — Apple grants this automatically for Finder Sync extensions
Cross-Platform
Yes, Finder Sync Extensions are macOS-only. Here's the equivalent plan per platform:

Windows
The equivalent of Finder Sync is a Shell Extension. Two tiers:

Simple (registry verbs) — for context menus on .mosaic files only:


HKEY_CLASSES_ROOT\.mosaic\shell\Delete from Network\command
  → "C:\Program Files\Mosaic\mosaic-handler.exe" delete "%1"
This is a few registry entries — very simple. You get right-click items on .mosaic files specifically. No badges.

Full (COM Shell Extension) — for badges + context menus on any file in a folder:

Implement IShellIconOverlayIdentifier (badges) and IContextMenu (right-click)
Written in C++ or C#
Same architecture as macOS: thin shell extension, talks to your Go daemon over a named pipe or localhost HTTP
Complexity is similar to Finder Sync
For Mosaic, the registry verb approach gets you 80% of the UX with 5% of the work on Windows — worth starting there.

Linux
No universal standard, but the big file managers all have extension mechanisms:

Dolphin (KDE) — easiest, just a .desktop file:


# ~/.local/share/kservices5/ServiceMenus/mosaic.desktop
[Desktop Entry]
Type=Service
MimeType=application/x-mosaic;
X-KDE-ServiceTypes=KonqPopupMenu/Plugin

[Desktop Action DeleteFromNetwork]
Name=Delete from Network
Exec=mosaic-cli delete %F

[Desktop Action FileInfo]
Name=File Info
Exec=mosaic-cli info %F
That's literally it for Dolphin. No code, just a config file.

Nautilus (GNOME) — Python extension:


# ~/.local/share/nautilus-python/extensions/mosaic.py
import subprocess
from gi.repository import Nautilus, GObject

class MosaicExtension(GObject.GObject, Nautilus.MenuProvider):
    def get_file_items(self, window, files):
        item = Nautilus.MenuItem(name="Mosaic::Delete", label="Delete from Network")
        item.connect("activate", self.delete, files)
        return [item]

    def delete(self, menu, files):
        for f in files:
            subprocess.Popen(["mosaic-cli", "delete", f.get_location().get_path()])
Thunar (XFCE): Custom Actions via GUI or XML config, similar simplicity to Dolphin.

Summary
Platform	Technology	Language	Complexity	Badges
macOS	Finder Sync Extension	Swift	Medium	Yes
Windows (simple)	Registry verbs	Registry	Very low	No
Windows (full)	COM Shell Extension	C++ / C#	High	Yes
Linux/KDE	Dolphin service menu	.desktop file	Very low	No
Linux/GNOME	Nautilus Python ext	Python	Low	No
The Go daemon is the same on all platforms — you just build thin platform-specific UI bridges that all talk to it. The daemon is where all your real work lives.
