import Cocoa

class MosaicDocumentController: NSDocumentController {
  override func openDocument(
    withContentsOf url: URL,
    display displayDocument: Bool,
    completionHandler: @escaping (NSDocument?, Bool, Error?) -> Void
  ) {
    guard url.pathExtension.lowercased() == "mosaic" else {
      super.openDocument(
        withContentsOf: url, display: displayDocument, completionHandler: completionHandler)
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
  private var overlay: DownloadOverlayController?
  private var uploadOverlay: UploadOverlayController?
  private var uploadWatchTimer: Timer?
  private var uploadWasActive = false
  private var statusWindow: NetworkStatusController?

  // Persisted preference: whether double-clicking a stub also opens the file.
  private var openAfterFetch: Bool {
    get { UserDefaults.standard.object(forKey: "openAfterFetch") as? Bool ?? true }
    set { UserDefaults.standard.set(newValue, forKey: "openAfterFetch") }
  }

  func applicationWillFinishLaunching(_ notification: Notification) {
    _ = MosaicDocumentController()
  }

  func applicationDidFinishLaunching(_ notification: Notification) {
    statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
    if let button = statusItem?.button {
      button.image = Self.makeBrandIcon()
    }
    buildMenu()
    watchMosaicDirectory()
    launchDaemon()
    startUploadWatcher()
  }

  // Called when the user clicks the Dock icon or the status-bar icon activates
  // the app. Raise the download overlay if one is active; otherwise fall back
  // to the network status window if nothing visible is already on screen.
  func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
    if let overlay = overlay, overlay.window?.isVisible == true {
      overlay.window?.orderFrontRegardless()
    } else if let up = uploadOverlay, up.window?.isVisible == true {
      up.window?.orderFrontRegardless()
    } else if !flag {
      openNetworkStatus()
    }
    return true
  }

  func applicationWillTerminate(_ notification: Notification) {
    uploadWatchTimer?.invalidate()
    dirWatcher?.cancel()
    daemonTask?.terminate()
  }

  // Poll /upload-progress every 300ms and show the upload overlay while active.
  private func startUploadWatcher() {
    let timer = Timer.scheduledTimer(withTimeInterval: 0.3, repeats: true) { [weak self] _ in
      guard let self else { return }
      DaemonClient.shared.getUploadProgress { progress in
        guard let progress else { return }
        DispatchQueue.main.async {
          let isActive = progress.total > 0 && progress.dispatched < progress.total
          if isActive && self.uploadOverlay == nil {
            // Fetch the filename from /active-op for the overlay title.
            DaemonClient.shared.getActiveOp { op in
              DispatchQueue.main.async {
                let filename = op?.description
                  .replacingOccurrences(of: "Uploading ", with: "") ?? "file"
                self.uploadOverlay = UploadOverlayController()
                self.uploadOverlay?.window?.orderFrontRegardless()
                self.uploadOverlay?.beginTracking(filename: filename)
                self.uploadWasActive = true
              }
            }
          } else if isActive, let overlay = self.uploadOverlay {
            overlay.update(dispatched: progress.dispatched, total: progress.total)
          } else if !isActive && self.uploadWasActive {
            self.uploadWasActive = false
            self.uploadOverlay?.finish()
            self.uploadOverlay = nil
          }
        }
      }
    }
    RunLoop.main.add(timer, forMode: .common)
    uploadWatchTimer = timer
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
      // Debounce: wait 200ms so rapid changes (stub delete + real file write) settle.
      DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) {
        self?.buildMenu()
      }
    }
    source.setCancelHandler {
      close(fd)
    }
    source.resume()
    dirWatcher = source
  }

  // MARK: - Status bar menu

  private func buildMenu() {
    let mosaicDir = FileManager.default.homeDirectoryForCurrentUser
      .appendingPathComponent("Mosaic")

    // Read the manifest — the authoritative list of network files.
    let manifestURL = mosaicDir.appendingPathComponent(".mosaic-manifest.json")
    let manifest: [String: ManifestEntry]
    if let data = try? Data(contentsOf: manifestURL),
      let decoded = try? JSONDecoder().decode([String: ManifestEntry].self, from: data)
    {
      manifest = decoded
    } else {
      manifest = [:]
    }

    let menu = NSMenu()
    menu.addItem(withTitle: "Mosaic", action: nil, keyEquivalent: "")
    menu.addItem(.separator())

    let networkStatusItem = NSMenuItem(
      title: "Open App",
      action: #selector(openNetworkStatus),
      keyEquivalent: "")
    networkStatusItem.target = self
    menu.addItem(networkStatusItem)

    menu.addItem(.separator())

    // Global preference: open files after fetching.
    let openAfterFetchItem = NSMenuItem(
      title: "Open files after fetching",
      action: #selector(toggleOpenAfterFetch),
      keyEquivalent: ""
    )
    openAfterFetchItem.target = self
    openAfterFetchItem.state = openAfterFetch ? .on : .off
    menu.addItem(openAfterFetchItem)
    menu.addItem(.separator())

    if manifest.isEmpty {
      menu.addItem(withTitle: "No files on network", action: nil, keyEquivalent: "")
    } else {
      for entry in manifest.values.sorted(by: { $0.name < $1.name }) {
        let filename = entry.name
        let item = NSMenuItem(title: filename, action: nil, keyEquivalent: "")
        let sub = NSMenu()

        let realFileExists = FileManager.default.fileExists(
          atPath: mosaicDir.appendingPathComponent(filename).path)
        let cached = realFileExists ? "Yes" : "No"
        sub.addItem(
          withTitle: "Size: \(Self.formatBytes(entry.size))", action: nil, keyEquivalent: "")
        sub.addItem(withTitle: "Added: \(entry.dateAdded)", action: nil, keyEquivalent: "")
        sub.addItem(withTitle: "Cached locally: \(cached)", action: nil, keyEquivalent: "")
        sub.addItem(.separator())

        if realFileExists {
          // File is cached — just open it.
          let openItem = NSMenuItem(
            title: "Open", action: #selector(openFileFromMenu(_:)), keyEquivalent: "")
          openItem.representedObject = filename
          openItem.target = self
          sub.addItem(openItem)
          sub.addItem(.separator())
          let refetchItem = NSMenuItem(
            title: "Re-fetch from network", action: #selector(cacheFileFromMenu(_:)), keyEquivalent: "")
          refetchItem.representedObject = filename
          refetchItem.target = self
          sub.addItem(refetchItem)
        } else {
          // File is remote-only — offer cache-only or cache-and-open.
          let cacheItem = NSMenuItem(
            title: "Download", action: #selector(cacheFileFromMenu(_:)), keyEquivalent: "")
          cacheItem.representedObject = filename
          cacheItem.target = self
          sub.addItem(cacheItem)
          sub.addItem(.separator())
          let cacheOpenItem = NSMenuItem(
            title: "Download & Open", action: #selector(cacheAndOpenFileFromMenu(_:)),
            keyEquivalent: "")
          cacheOpenItem.representedObject = filename
          cacheOpenItem.target = self
          sub.addItem(cacheOpenItem)
        }

        item.submenu = sub
        menu.addItem(item)
      }
    }

    menu.addItem(.separator())
    menu.addItem(withTitle: "Quit Mosaic", action: #selector(quitMosaic), keyEquivalent: "q")

    statusItem?.menu = menu
  }

  // MARK: - Menu actions

  @objc private func openNetworkStatus() {
    if statusWindow == nil {
      statusWindow = NetworkStatusController()
    }
    statusWindow?.window?.orderFrontRegardless()
    statusWindow?.startRefreshing()
  }

@objc private func toggleOpenAfterFetch() {
    openAfterFetch.toggle()
    buildMenu()
  }

  @objc private func openFileFromMenu(_ sender: NSMenuItem) {
    guard let filename = sender.representedObject as? String else { return }
    let realURL = FileManager.default.homeDirectoryForCurrentUser
      .appendingPathComponent("Mosaic")
      .appendingPathComponent(filename)
    openFile(realURL)
  }

  // Cache only — no open.
  @objc private func cacheFileFromMenu(_ sender: NSMenuItem) {
    guard let filename = sender.representedObject as? String else { return }
    fetchWithOverlay(filename: filename)
  }

  // Cache and open.
  @objc private func cacheAndOpenFileFromMenu(_ sender: NSMenuItem) {
    guard let filename = sender.representedObject as? String else { return }
    let realURL = FileManager.default.homeDirectoryForCurrentUser
      .appendingPathComponent("Mosaic")
      .appendingPathComponent(filename)
    fetchAndOpen(filename: filename, realURL: realURL)
  }

  // MARK: - File open (called from double-clicking a stub)

  // Primary entry point for Finder file-open events in LSUIElement apps.
  // NSDocumentController's openDocument path is not reliably called for
  // status-bar-only apps; this delegate method is the correct hook.
  func application(_ application: NSApplication, open urls: [URL]) {
    for url in urls {
      guard url.pathExtension.lowercased() == "mosaic" else { continue }
      handleOpen(path: url.path)
    }
  }

  func handleOpen(path: String) {
    let url = URL(fileURLWithPath: path)
    let filename = url.deletingPathExtension().lastPathComponent
    let realURL = FileManager.default.homeDirectoryForCurrentUser
      .appendingPathComponent("Mosaic")
      .appendingPathComponent(filename)

    if FileManager.default.fileExists(atPath: realURL.path) {
      openFile(realURL)
    } else if openAfterFetch {
      fetchAndOpen(filename: filename, realURL: realURL)
    } else {
      DaemonClient.shared.fetch(filename) { _ in }
    }
  }

private func fetchAndOpen(filename: String, realURL: URL) {
    fetchWithOverlay(filename: filename, openWhenDone: realURL)
}

// Shows the download overlay while fetching filename.
// If openWhenDone is non-nil, opens that URL once the file lands on disk.
// completion is called on the main queue when the fetch request returns.
func fetchWithOverlay(filename: String, openWhenDone realURL: URL? = nil, completion: (() -> Void)? = nil) {
    overlay = DownloadOverlayController()
    overlay?.window?.orderFrontRegardless()
    overlay?.beginTracking(filename: filename)

    var pollTimer: Timer?

    // finish() closes the window after 1.2s; nil the reference after 1.5s so
    // applicationShouldHandleReopen can't resurrect the closed window.
    let finishOverlay = { [weak self] in
        self?.overlay?.finish()
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { [weak self] in
            self?.overlay = nil
        }
    }

    DaemonClient.shared.fetch(filename) { _ in
        DispatchQueue.main.async {
            pollTimer?.invalidate()
            pollTimer = nil
            finishOverlay()
            completion?()
            if let url = realURL {
                if FileManager.default.fileExists(atPath: url.path) {
                    self.openFile(url)
                } else {
                    let alert = NSAlert()
                    alert.messageText = "Could not fetch \(filename)"
                    alert.informativeText = "The daemon could not retrieve this file from the network."
                    alert.runModal()
                }
            }
        }
    }

    let timer = Timer.scheduledTimer(withTimeInterval: 0.15, repeats: true) { t in
        DaemonClient.shared.getDownloadProgress { progress in
            guard let progress else { return }
            DispatchQueue.main.async {
                self.overlay?.update(
                    received: progress.received,
                    needed: progress.needed
                )
                if progress.needed > 0 && progress.received >= progress.needed {
                    t.invalidate()
                    pollTimer = nil
                    finishOverlay()
                }
            }
        }
    }
    pollTimer = timer
    RunLoop.main.add(timer, forMode: .common)
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

  // Draws a flat hexagon in the logo's light blue (#29ABE2), matching the
  // brand mark in docs/assets/logo.png. isTemplate = false keeps the color
  // visible in both light and dark menu bars.
  // Dark smoked-glass hexagon: dark fill, a lighter outline, and a narrow
  // 45-degree light sweep through the centre — like light catching glass.
  private static func makeBrandIcon() -> NSImage {
    let size = NSSize(width: 18, height: 18)
    let img = NSImage(size: size, flipped: false) { rect in
      let cx = rect.midX, cy = rect.midY
      let r: CGFloat = 8.0
      let hex = NSBezierPath()
      for i in 0..<6 {
        let angle = CGFloat(i) * .pi / 3 + .pi / 6
        let pt = NSPoint(x: cx + r * cos(angle), y: cy + r * sin(angle))
        if i == 0 { hex.move(to: pt) } else { hex.line(to: pt) }
      }
      hex.close()

      // Diagonal gradient: dark top-left → lighter bottom-right.
      NSGraphicsContext.current?.saveGraphicsState()
      hex.setClip()
      if let gradient = NSGradient(
        starting: NSColor(white: 0.10, alpha: 1.0),
        ending: NSColor(white: 0.70, alpha: 1.0)
      ) {
        gradient.draw(in: rect, angle: -45)
      }
      NSGraphicsContext.current?.restoreGraphicsState()

      // Black border.
      hex.lineWidth = 1.2
      NSColor.black.setStroke()
      hex.stroke()

      return true
    }
    img.isTemplate = false
    return img
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
    guard
      let execPath = candidates.first(where: { FileManager.default.isExecutableFile(atPath: $0) })
    else {
      return
    }
    let logURL = mosaicLogFileURL
    FileManager.default.createFile(atPath: logURL.path, contents: nil)
    let logHandle = try? FileHandle(forWritingTo: logURL)
    logHandle?.seekToEndOfFile()

    let task = Process()
    task.executableURL = URL(fileURLWithPath: execPath)
    task.standardOutput = logHandle
    task.standardError  = logHandle
    try? task.run()
    daemonTask = task
  }
}
