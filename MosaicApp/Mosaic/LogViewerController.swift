import Cocoa

// MARK: - Log file path (shared with AppDelegate's daemon launcher)
// Must match LOG_FILE in scripts/install.sh.
let mosaicLogFileURL: URL = FileManager.default.homeDirectoryForCurrentUser
    .appendingPathComponent("Mosaic/.mosaic-daemon.log")

// MARK: - Window Controller

class LogViewerController: NSWindowController, NSWindowDelegate {
    private var contentVC: LogViewerViewController?

    init() {
        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 720, height: 500),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        win.title = "Mosaic Logs"
        win.minSize = NSSize(width: 500, height: 300)
        win.center()
        win.setFrameAutosaveName("MosaicLogViewer")
        win.collectionBehavior = [.fullScreenPrimary]
        super.init(window: win)
        let vc = LogViewerViewController()
        win.contentViewController = vc
        contentVC = vc
        win.delegate = self
    }

    required init?(coder: NSCoder) { fatalError() }

    func startTailing() { contentVC?.startTailing() }
    func stopTailing()  { contentVC?.stopTailing()  }
    func windowWillClose(_ notification: Notification) { stopTailing() }
}

// MARK: - View Controller

private class LogViewerViewController: NSViewController {
    private var textView: NSTextView!
    private var scrollView: NSScrollView!
    private var tailTimer: Timer?
    private var lastReadOffset: UInt64 = 0
    private var autoScroll = true

    private let bgColor   = NSColor(red: 14/255,  green: 17/255,  blue: 23/255,  alpha: 1)
    private let textColor = NSColor(red: 204/255, green: 204/255, blue: 204/255, alpha: 1)

    private let tagColors: [(prefix: String, color: NSColor)] = [
        ("[Transfer]",  NSColor(red: 41/255,  green: 171/255, blue: 226/255, alpha: 1)),
        ("[Recv]",      NSColor(red: 98/255,  green: 200/255, blue: 155/255, alpha: 1)),
        ("[Manifest]",  NSColor(red: 255/255, green: 189/255, blue: 68/255,  alpha: 1)),
        ("[Daemon]",    NSColor(red: 179/255, green: 135/255, blue: 255/255, alpha: 1)),
        ("[p2p]",       NSColor(red: 255/255, green: 135/255, blue: 135/255, alpha: 1)),
        ("Warning",     NSColor(red: 255/255, green: 165/255, blue: 0/255,   alpha: 1)),
        ("Error",       NSColor(red: 255/255, green: 90/255,  blue: 90/255,  alpha: 1)),
    ]

    override func loadView() {
        view = NSView(frame: NSRect(x: 0, y: 0, width: 720, height: 500))
        view.wantsLayer = true
        view.layer?.backgroundColor = bgColor.cgColor
        buildUI()
    }

    private func buildUI() {
        let toolbar = NSView()
        toolbar.wantsLayer = true
        toolbar.layer?.backgroundColor = NSColor(white: 0.08, alpha: 1).cgColor
        toolbar.translatesAutoresizingMaskIntoConstraints = false
        // Force dark appearance so buttons and checkbox render with white text
        // on the dark toolbar background instead of inheriting the window's light mode.
        toolbar.appearance = NSAppearance(named: .darkAqua)
        view.addSubview(toolbar)

        let titleLbl = NSTextField(labelWithString: "Daemon Logs")
        titleLbl.font = .systemFont(ofSize: 13, weight: .semibold)
        titleLbl.textColor = .white
        titleLbl.translatesAutoresizingMaskIntoConstraints = false
        toolbar.addSubview(titleLbl)

        let pathLbl = NSTextField(labelWithString: mosaicLogFileURL.path)
        pathLbl.font = .monospacedSystemFont(ofSize: 9, weight: .regular)
        pathLbl.textColor = NSColor.white.withAlphaComponent(0.4)
        pathLbl.translatesAutoresizingMaskIntoConstraints = false
        toolbar.addSubview(pathLbl)

        let clearBtn = NSButton(title: "Clear", target: self, action: #selector(clearLog))
        clearBtn.bezelStyle = .rounded
        clearBtn.controlSize = .small
        clearBtn.translatesAutoresizingMaskIntoConstraints = false
        toolbar.addSubview(clearBtn)

        let copyBtn = NSButton(title: "Copy All", target: self, action: #selector(copyLog))
        copyBtn.bezelStyle = .rounded
        copyBtn.controlSize = .small
        copyBtn.translatesAutoresizingMaskIntoConstraints = false
        toolbar.addSubview(copyBtn)

        let autoScrollCheck = NSButton(checkboxWithTitle: "Auto-scroll",
                                       target: self, action: #selector(toggleAutoScroll(_:)))
        autoScrollCheck.state = .on
        autoScrollCheck.translatesAutoresizingMaskIntoConstraints = false
        toolbar.addSubview(autoScrollCheck)

        scrollView = NSScrollView()
        scrollView.translatesAutoresizingMaskIntoConstraints = false
        scrollView.hasVerticalScroller   = true
        scrollView.hasHorizontalScroller = true
        scrollView.autohidesScrollers    = true
        scrollView.backgroundColor       = bgColor
        view.addSubview(scrollView)

        textView = NSTextView()
        textView.isEditable             = false
        textView.isSelectable           = true
        textView.backgroundColor        = bgColor
        textView.drawsBackground        = true
        textView.font                   = .monospacedSystemFont(ofSize: 11, weight: .regular)
        textView.textColor              = textColor
        textView.textContainerInset     = NSSize(width: 10, height: 10)
        textView.isVerticallyResizable  = true
        textView.isHorizontallyResizable = true
        textView.textContainer?.widthTracksTextView  = false
        textView.textContainer?.heightTracksTextView = false
        textView.textContainer?.containerSize = NSSize(
            width: CGFloat.greatestFiniteMagnitude,
            height: CGFloat.greatestFiniteMagnitude)
        scrollView.documentView = textView

        NSLayoutConstraint.activate([
            toolbar.topAnchor.constraint(equalTo: view.topAnchor),
            toolbar.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            toolbar.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            toolbar.heightAnchor.constraint(equalToConstant: 48),

            titleLbl.leadingAnchor.constraint(equalTo: toolbar.leadingAnchor, constant: 14),
            titleLbl.topAnchor.constraint(equalTo: toolbar.topAnchor, constant: 8),

            pathLbl.leadingAnchor.constraint(equalTo: titleLbl.leadingAnchor),
            pathLbl.topAnchor.constraint(equalTo: titleLbl.bottomAnchor, constant: 2),

            clearBtn.trailingAnchor.constraint(equalTo: toolbar.trailingAnchor, constant: -12),
            clearBtn.centerYAnchor.constraint(equalTo: toolbar.centerYAnchor),

            copyBtn.trailingAnchor.constraint(equalTo: clearBtn.leadingAnchor, constant: -8),
            copyBtn.centerYAnchor.constraint(equalTo: toolbar.centerYAnchor),

            autoScrollCheck.trailingAnchor.constraint(equalTo: copyBtn.leadingAnchor, constant: -12),
            autoScrollCheck.centerYAnchor.constraint(equalTo: toolbar.centerYAnchor),

            scrollView.topAnchor.constraint(equalTo: toolbar.bottomAnchor),
            scrollView.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            scrollView.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            scrollView.bottomAnchor.constraint(equalTo: view.bottomAnchor),
        ])
    }

    // MARK: - Tailing

    func startTailing() {
        lastReadOffset = 0
        loadFullLog()
        let t = Timer.scheduledTimer(withTimeInterval: 0.5, repeats: true) { [weak self] _ in
            self?.pollLog()
        }
        RunLoop.main.add(t, forMode: .common)
        tailTimer = t
    }

    func stopTailing() {
        tailTimer?.invalidate()
        tailTimer = nil
    }

    private func logFileData() -> Data? {
        // Ensure the log file exists so the daemon can write to it.
        let url = mosaicLogFileURL
        if !FileManager.default.fileExists(atPath: url.path) {
            try? "".write(to: url, atomically: true, encoding: .utf8)
        }
        return try? Data(contentsOf: url)
    }

    private func loadFullLog() {
        guard let data = logFileData() else { return }
        lastReadOffset = UInt64(data.count)
        let text = String(data: data, encoding: .utf8) ?? ""
        if text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            let placeholder = attributed("— No logs yet. Start the daemon to see output. —\n",
                                         color: NSColor.white.withAlphaComponent(0.3))
            textView.textStorage?.setAttributedString(placeholder)
            return
        }
        let lines = text.components(separatedBy: "\n")
        textView.textStorage?.setAttributedString(buildAttributed(lines: lines))
        if autoScroll { scrollToBottom() }
    }

    private func pollLog() {
        let url = mosaicLogFileURL
        guard let attrs = try? FileManager.default.attributesOfItem(atPath: url.path),
              let size = attrs[.size] as? UInt64
        else {
            // File was deleted — reset
            lastReadOffset = 0
            return
        }

        if size < lastReadOffset {
            // File was truncated (cleared)
            lastReadOffset = 0
            loadFullLog()
            return
        }
        guard size > lastReadOffset else { return }

        guard let fh = try? FileHandle(forReadingFrom: url) else { return }
        defer { try? fh.close() }
        try? fh.seek(toOffset: lastReadOffset)
        let newData = fh.readDataToEndOfFile()
        lastReadOffset = size

        guard !newData.isEmpty else { return }
        let newText = String(data: newData, encoding: .utf8) ?? ""

        // If the view currently shows only the placeholder, replace it.
        let currentText = textView.textStorage?.string ?? ""
        if currentText.hasPrefix("— No logs yet") {
            textView.textStorage?.setAttributedString(NSAttributedString())
        }

        let lines = newText.components(separatedBy: "\n")
        textView.textStorage?.append(buildAttributed(lines: lines))
        if autoScroll { scrollToBottom() }
    }

    private func buildAttributed(lines: [String]) -> NSAttributedString {
        let result = NSMutableAttributedString()
        for line in lines {
            result.append(attributed(line + "\n", color: colorForLine(line)))
        }
        return result
    }

    private func attributed(_ text: String, color: NSColor) -> NSAttributedString {
        NSAttributedString(string: text, attributes: [
            .font: NSFont.monospacedSystemFont(ofSize: 11, weight: .regular),
            .foregroundColor: color,
        ])
    }

    private func colorForLine(_ line: String) -> NSColor {
        for entry in tagColors {
            if line.contains(entry.prefix) { return entry.color }
        }
        return textColor
    }

    private func scrollToBottom() {
        guard let len = textView.textStorage?.length, len > 0 else { return }
        textView.scrollRangeToVisible(NSRange(location: len, length: 0))
    }

    @objc private func copyLog() {
        let text = textView.textStorage?.string ?? ""
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
    }

    @objc private func clearLog() {
        // Truncate in-place so the daemon's open FileHandle keeps writing to the same
        // inode. An atomic write would swap the inode, orphaning the daemon's handle
        // and causing all new log output to disappear from the viewer.
        if let fh = try? FileHandle(forWritingTo: mosaicLogFileURL) {
            try? fh.truncate(atOffset: 0)
            try? fh.close()
        }
        textView.textStorage?.setAttributedString(NSAttributedString())
        lastReadOffset = 0
    }

    @objc private func toggleAutoScroll(_ sender: NSButton) {
        autoScroll = sender.state == .on
    }
}
