import Cocoa

// MARK: - Window Controller

class NetworkStatusController: NSWindowController, NSWindowDelegate {
    private var contentVC: NetworkStatusViewController?

    init() {
        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 700, height: 580),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        win.title = "Mosaic Network"
        win.minSize = NSSize(width: 520, height: 440)
        win.center()
        win.setFrameAutosaveName("MosaicNetworkStatus")
        win.collectionBehavior = [.fullScreenPrimary]
        super.init(window: win)
        let vc = NetworkStatusViewController()
        win.contentViewController = vc
        contentVC = vc
        win.delegate = self
    }

    required init?(coder: NSCoder) { fatalError() }

    func startRefreshing() { contentVC?.startRefreshing() }
    func stopRefreshing()  { contentVC?.stopRefreshing()  }
    func windowWillClose(_ notification: Notification) { stopRefreshing() }
}

// MARK: - Gradient header

private class StatusHeaderView: NSView {
    override func draw(_ dirtyRect: NSRect) {
        let top = NSColor(red: 12/255,  green: 65/255,  blue: 105/255, alpha: 1)
        let bot = NSColor(red: 30/255,  green: 130/255, blue: 180/255, alpha: 1)
        NSGradient(starting: top, ending: bot)?.draw(in: bounds, angle: 270)
        NSColor(white: 0, alpha: 0.2).setFill()
        NSRect(x: 0, y: 0, width: bounds.width, height: 1).fill()
    }
}

// MARK: - Card view

private class StatusCardView: NSView {
    override init(frame: NSRect) { super.init(frame: frame); wantsLayer = true }
    required init?(coder: NSCoder) { fatalError() }
    override func updateLayer() {
        layer?.backgroundColor = NSColor.controlBackgroundColor.cgColor
        layer?.cornerRadius = 10
        layer?.borderWidth = 0.5
        layer?.borderColor = NSColor.separatorColor.cgColor
    }
}

// MARK: - View Controller

class NetworkStatusViewController: NSViewController {

    // Header
    private let connDot   = lbl("●", 13, .regular, .systemGray)
    private let connLabel = lbl("Connecting…", 12, .semibold, .white)

    // Stat numbers
    private let peerNum   = lbl("—", 28, .bold)
    private let fileNum   = lbl("—", 28, .bold)
    private let cachedNum = lbl("—", 28, .bold)

    // Lists
    private let peerStack = NSStackView()
    private let fileStack = NSStackView()

    // Activity
    private var activityCard: NSView!
    private let activityDesc = lbl("", 12, .regular, .secondaryLabelColor)
    private let activityBar  = NSProgressIndicator()

    private var scrollView: NSScrollView!
    private var refreshTimer: Timer?
    private var logWindow: LogViewerController?

    private let blue     = NSColor(red: 41/255, green: 171/255, blue: 226/255, alpha: 1)
    private let darkBlue = NSColor(red: 27/255, green: 124/255, blue: 171/255, alpha: 1)

    override func loadView() {
        view = NSView()
        view.wantsLayer = true
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        buildHeader()
        buildBody()
    }

    // MARK: - Header

    private func buildHeader() {
        let hdr = StatusHeaderView()
        hdr.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(hdr)

        let title    = lbl("Mosaic", 20, .bold, .white)
        let subtitle = lbl("Distributed File System", 11, .regular,
                           NSColor.white.withAlphaComponent(0.55))

        let logsBtn = NSButton(title: "Logs", target: self, action: #selector(openLogs))
        logsBtn.bezelStyle = .rounded
        logsBtn.controlSize = .small
        logsBtn.translatesAutoresizingMaskIntoConstraints = false

        for v in [title, subtitle, connDot, connLabel, logsBtn] { hdr.addSubview(v) }

        NSLayoutConstraint.activate([
            hdr.topAnchor.constraint(equalTo: view.topAnchor),
            hdr.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            hdr.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            hdr.heightAnchor.constraint(equalToConstant: 88),

            title.leadingAnchor.constraint(equalTo: hdr.leadingAnchor, constant: 24),
            title.topAnchor.constraint(equalTo: hdr.topAnchor, constant: 24),

            subtitle.leadingAnchor.constraint(equalTo: title.leadingAnchor),
            subtitle.topAnchor.constraint(equalTo: title.bottomAnchor, constant: 3),

            logsBtn.trailingAnchor.constraint(equalTo: hdr.trailingAnchor, constant: -24),
            logsBtn.bottomAnchor.constraint(equalTo: hdr.bottomAnchor, constant: -14),

            connLabel.trailingAnchor.constraint(equalTo: hdr.trailingAnchor, constant: -24),
            connLabel.topAnchor.constraint(equalTo: hdr.topAnchor, constant: 24),

            connDot.trailingAnchor.constraint(equalTo: connLabel.leadingAnchor, constant: -5),
            connDot.centerYAnchor.constraint(equalTo: connLabel.centerYAnchor),
        ])
    }

    // MARK: - Body

    private func buildBody() {
        peerStack.orientation = .vertical
        peerStack.alignment   = .leading
        peerStack.spacing     = 8

        fileStack.orientation = .vertical
        fileStack.alignment   = .leading
        fileStack.spacing     = 0

        activityCard = buildActivityCard()
        activityCard.isHidden = true

        let body = NSStackView(views: [
            buildStatsRow(),
            buildCard("CONNECTED PEERS", content: peerStack),
            buildCard("NETWORK FILES",   content: fileStack),
            activityCard,
        ])
        body.orientation  = .vertical
        body.alignment    = .leading
        body.spacing      = 16
        body.edgeInsets   = NSEdgeInsets(top: 20, left: 20, bottom: 24, right: 20)
        body.translatesAutoresizingMaskIntoConstraints = false

        scrollView = NSScrollView()
        scrollView.translatesAutoresizingMaskIntoConstraints = false
        scrollView.hasVerticalScroller    = true
        scrollView.hasHorizontalScroller  = false
        scrollView.autohidesScrollers     = true
        scrollView.drawsBackground        = false
        scrollView.documentView           = body
        view.addSubview(scrollView)

        NSLayoutConstraint.activate([
            scrollView.topAnchor.constraint(equalTo: view.topAnchor, constant: 88),
            scrollView.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            scrollView.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            scrollView.bottomAnchor.constraint(equalTo: view.bottomAnchor),

            body.topAnchor.constraint(equalTo: scrollView.contentView.topAnchor),
            body.leadingAnchor.constraint(equalTo: scrollView.contentView.leadingAnchor),
            body.trailingAnchor.constraint(equalTo: scrollView.contentView.trailingAnchor),
        ])

        // With .leading alignment arranged subviews don't auto-fill width.
        // Pin each card to body.width minus the left+right edgeInsets (20+20).
        for sv in body.arrangedSubviews {
            sv.widthAnchor.constraint(equalTo: body.widthAnchor, constant: -40).isActive = true
        }
    }

    // MARK: - Stats row

    private func buildStatsRow() -> NSView {
        func statCard(_ num: NSTextField, caption: String) -> NSView {
            let c = StatusCardView()
            c.translatesAutoresizingMaskIntoConstraints = false
            num.translatesAutoresizingMaskIntoConstraints = false
            num.textColor = darkBlue
            let cap = lbl(caption, 9, .semibold, .tertiaryLabelColor)
            c.addSubview(num); c.addSubview(cap)
            NSLayoutConstraint.activate([
                num.topAnchor.constraint(equalTo: c.topAnchor, constant: 14),
                num.leadingAnchor.constraint(equalTo: c.leadingAnchor, constant: 16),
                cap.topAnchor.constraint(equalTo: num.bottomAnchor, constant: 2),
                cap.leadingAnchor.constraint(equalTo: num.leadingAnchor),
                cap.bottomAnchor.constraint(equalTo: c.bottomAnchor, constant: -14),
            ])
            return c
        }
        let row = NSStackView(views: [
            statCard(peerNum,   caption: "PEERS"),
            statCard(fileNum,   caption: "FILES"),
            statCard(cachedNum, caption: "CACHED"),
        ])
        row.orientation  = .horizontal
        row.distribution = .fillEqually
        row.spacing      = 12
        return row
    }

    // MARK: - Content card

    private func buildCard(_ title: String, content: NSView) -> NSView {
        let card = StatusCardView()
        card.translatesAutoresizingMaskIntoConstraints = false

        let hdr = lbl(title, 10, .semibold, .tertiaryLabelColor)
        let sep = NSBox(); sep.boxType = .separator
        sep.translatesAutoresizingMaskIntoConstraints = false
        content.translatesAutoresizingMaskIntoConstraints = false

        card.addSubview(hdr); card.addSubview(sep); card.addSubview(content)
        NSLayoutConstraint.activate([
            hdr.topAnchor.constraint(equalTo: card.topAnchor, constant: 14),
            hdr.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 16),
            hdr.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -16),

            sep.topAnchor.constraint(equalTo: hdr.bottomAnchor, constant: 10),
            sep.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 16),
            sep.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -16),

            content.topAnchor.constraint(equalTo: sep.bottomAnchor, constant: 12),
            content.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 16),
            content.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -16),
            content.bottomAnchor.constraint(equalTo: card.bottomAnchor, constant: -14),
        ])
        return card
    }

    // MARK: - Activity card

    private func buildActivityCard() -> NSView {
        let card = StatusCardView()
        card.translatesAutoresizingMaskIntoConstraints = false

        let hdr = lbl("ACTIVE OPERATION", 10, .semibold, .tertiaryLabelColor)
        let sep = NSBox(); sep.boxType = .separator
        sep.translatesAutoresizingMaskIntoConstraints = false
        activityDesc.translatesAutoresizingMaskIntoConstraints = false

        activityBar.style = .bar
        activityBar.minValue = 0; activityBar.maxValue = 100
        activityBar.isIndeterminate = true
        activityBar.translatesAutoresizingMaskIntoConstraints = false
        activityBar.startAnimation(nil)

        for v in [hdr, sep, activityDesc, activityBar] { card.addSubview(v) }
        NSLayoutConstraint.activate([
            hdr.topAnchor.constraint(equalTo: card.topAnchor, constant: 14),
            hdr.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 16),
            hdr.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -16),

            sep.topAnchor.constraint(equalTo: hdr.bottomAnchor, constant: 10),
            sep.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 16),
            sep.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -16),

            activityDesc.topAnchor.constraint(equalTo: sep.bottomAnchor, constant: 12),
            activityDesc.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 16),
            activityDesc.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -16),

            activityBar.topAnchor.constraint(equalTo: activityDesc.bottomAnchor, constant: 10),
            activityBar.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 16),
            activityBar.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -16),
            activityBar.bottomAnchor.constraint(equalTo: card.bottomAnchor, constant: -14),
        ])
        return card
    }

    // MARK: - Log viewer

    @objc private func openLogs() {
        if logWindow == nil {
            logWindow = LogViewerController()
        }
        logWindow?.window?.orderFrontRegardless()
        logWindow?.startTailing()
    }

    // MARK: - Refresh

    func startRefreshing() {
        refresh()
        let t = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in
            self?.refresh()
        }
        RunLoop.main.add(t, forMode: .common)
        refreshTimer = t
    }

    func stopRefreshing() { refreshTimer?.invalidate(); refreshTimer = nil }

    private func refresh() {
        DaemonClient.shared.getNetworkStatus { [weak self] s in
            DispatchQueue.main.async { self?.applyNetworkStatus(s) }
        }
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            self?.loadManifest()
        }
        DaemonClient.shared.getActiveOp { [weak self] op in
            DispatchQueue.main.async { self?.applyActiveOp(op) }
        }
    }

    // MARK: - Data application

    private func applyNetworkStatus(_ s: DaemonClient.NetworkStatus?) {
        guard let s else {
            connDot.textColor     = .systemOrange
            connLabel.stringValue = "Daemon offline"
            peerNum.stringValue   = "—"
            updatePeerRows([])
            return
        }
        connDot.textColor     = s.connected ? .systemGreen : .systemRed
        connLabel.stringValue = s.connected
            ? "Connected · \(s.peerCount) \(s.peerCount == 1 ? "peer" : "peers")"
            : "Not connected"
        peerNum.stringValue   = "\(s.peerCount)"
        updatePeerRows(s.peers)
    }

    private func updatePeerRows(_ peers: [DaemonClient.PeerEntry]) {
        peerStack.arrangedSubviews.forEach { $0.removeFromSuperview() }
        if peers.isEmpty {
            peerStack.addArrangedSubview(lbl("No peers connected", 12, .regular, .tertiaryLabelColor))
            return
        }
        for p in peers {
            let row = NSStackView()
            row.spacing = 10

            let idField = NSTextField(labelWithString: String(p.id.prefix(16)))
            idField.font = .monospacedSystemFont(ofSize: 11, weight: .regular)
            idField.textColor = .labelColor
            idField.translatesAutoresizingMaskIntoConstraints = false

            let host = p.address.components(separatedBy: ":").first ?? p.address

            row.addArrangedSubview(lbl("◦", 14, .regular, blue))
            row.addArrangedSubview(idField)
            row.addArrangedSubview(lbl(host, 10, .regular, .secondaryLabelColor))
            row.addArrangedSubview(badge(p.viaTURN ? "TURN" : "Direct",
                                         fg: p.viaTURN ? .systemOrange : .systemGreen))
            row.addArrangedSubview(badge(p.quicActive ? "QUIC" : "UDP",
                                         fg: p.quicActive ? .systemPurple : .secondaryLabelColor))
            peerStack.addArrangedSubview(row)
        }
    }

    private func loadManifest() {
        let mosaicDir = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Mosaic")
        let url = mosaicDir.appendingPathComponent(".mosaic-manifest.json")
        guard let data = try? Data(contentsOf: url),
              let manifest = try? JSONDecoder().decode([String: ManifestEntry].self, from: data)
        else {
            DispatchQueue.main.async {
                self.fileNum.stringValue   = "0"
                self.cachedNum.stringValue = "0"
                self.updateFileRows([], mosaicDir: mosaicDir)
            }
            return
        }
        let entries = Array(manifest.values).sorted { $0.name < $1.name }
        let cached  = entries.filter {
            FileManager.default.fileExists(
                atPath: mosaicDir.appendingPathComponent($0.name).path)
        }.count
        DispatchQueue.main.async {
            self.fileNum.stringValue   = "\(entries.count)"
            self.cachedNum.stringValue = "\(cached)"
            self.updateFileRows(entries, mosaicDir: mosaicDir)
        }
    }

    private func updateFileRows(_ entries: [ManifestEntry], mosaicDir: URL) {
        fileStack.arrangedSubviews.forEach { $0.removeFromSuperview() }
        if entries.isEmpty {
            fileStack.addArrangedSubview(lbl("No files on network", 12, .regular, .tertiaryLabelColor))
            return
        }
        for (i, entry) in entries.enumerated() {
            if i > 0 {
                let sep = NSBox(); sep.boxType = .separator
                sep.translatesAutoresizingMaskIntoConstraints = false
                fileStack.addArrangedSubview(sep)
            }
            let isCached = FileManager.default.fileExists(
                atPath: mosaicDir.appendingPathComponent(entry.name).path)

            let row = NSStackView()
            row.spacing = 8

            let nameLbl = lbl(entry.name, 12, .medium, .labelColor)
            nameLbl.lineBreakMode = .byTruncatingMiddle
            nameLbl.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)

            let spacer = NSView()
            spacer.setContentHuggingPriority(.defaultLow, for: .horizontal)
            spacer.translatesAutoresizingMaskIntoConstraints = false

            let actionBtn = NSButton(title: isCached ? "Open" : "Cache",
                                     target: self, action: isCached
                                        ? #selector(openFileTapped(_:))
                                        : #selector(cacheFileTapped(_:)))
            actionBtn.bezelStyle = .rounded
            actionBtn.controlSize = .small
            actionBtn.identifier = NSUserInterfaceItemIdentifier(entry.name)

            row.addArrangedSubview(nameLbl)
            row.addArrangedSubview(spacer)
            row.addArrangedSubview(lbl(formatBytes(entry.size), 11, .regular, .secondaryLabelColor))
            row.addArrangedSubview(actionBtn)
            fileStack.addArrangedSubview(row)
            row.widthAnchor.constraint(equalTo: fileStack.widthAnchor).isActive = true
        }
    }

    @objc private func cacheFileTapped(_ sender: NSButton) {
        let name = sender.identifier?.rawValue ?? ""
        sender.isEnabled = false
        DaemonClient.shared.fetch(name) { [weak self] _ in
            DispatchQueue.main.async { self?.loadManifest() }
        }
    }

    @objc private func openFileTapped(_ sender: NSButton) {
        let name = sender.identifier?.rawValue ?? ""
        let url = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Mosaic")
            .appendingPathComponent(name)
        NSWorkspace.shared.open(url)
    }

    private func applyActiveOp(_ op: DaemonClient.ActiveOp?) {
        guard let op else { activityCard.isHidden = true; return }
        activityCard.isHidden = false
        activityDesc.stringValue = op.description
        if op.kind == "upload" {
            DaemonClient.shared.getUploadProgress { [weak self] p in
                DispatchQueue.main.async {
                    self?.applyActivityBar(received: p?.dispatched ?? 0, needed: p?.total ?? 0)
                }
            }
        } else {
            DaemonClient.shared.getDownloadProgress { [weak self] p in
                DispatchQueue.main.async {
                    self?.applyActivityBar(received: p?.received ?? 0, needed: p?.needed ?? 0)
                }
            }
        }
    }

    private func applyActivityBar(received: Int, needed: Int) {
        if needed > 0 {
            activityBar.isIndeterminate = false
            activityBar.stopAnimation(nil)
            activityBar.doubleValue = Double(received) / Double(needed) * 100
        } else {
            activityBar.isIndeterminate = true
            activityBar.startAnimation(nil)
        }
    }

    // MARK: - Helpers

    private func badge(_ text: String, fg: NSColor) -> NSView {
        let c = NSView()
        c.wantsLayer = true
        c.layer?.cornerRadius = 4
        c.layer?.backgroundColor = fg.withAlphaComponent(0.15).cgColor
        c.translatesAutoresizingMaskIntoConstraints = false
        let l = lbl(text, 9, .semibold, fg)
        c.addSubview(l)
        NSLayoutConstraint.activate([
            l.topAnchor.constraint(equalTo: c.topAnchor, constant: 3),
            l.bottomAnchor.constraint(equalTo: c.bottomAnchor, constant: -3),
            l.leadingAnchor.constraint(equalTo: c.leadingAnchor, constant: 6),
            l.trailingAnchor.constraint(equalTo: c.trailingAnchor, constant: -6),
        ])
        return c
    }

    private func formatBytes(_ bytes: Int) -> String {
        guard bytes > 0 else { return "—" }
        let kb = Double(bytes) / 1024
        let mb = kb / 1024
        let gb = mb / 1024
        if gb >= 1 { return String(format: "%.1f GB", gb) }
        if mb >= 1 { return String(format: "%.1f MB", mb) }
        return String(format: "%.0f KB", kb)
    }
}

// MARK: - Free helper (usable in stored-property defaults)

private func lbl(
    _ text: String,
    _ size: CGFloat,
    _ weight: NSFont.Weight = .regular,
    _ color: NSColor = .labelColor
) -> NSTextField {
    let f = NSTextField(labelWithString: text)
    f.font = .systemFont(ofSize: size, weight: weight)
    f.textColor = color
    f.translatesAutoresizingMaskIntoConstraints = false
    return f
}
