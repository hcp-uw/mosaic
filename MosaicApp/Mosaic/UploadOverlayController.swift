import Cocoa

class UploadOverlayController: NSWindowController {

    private let filenameLabel = NSTextField(labelWithString: "")
    private let progressBar = NSProgressIndicator()
    private let statsLabel = NSTextField(labelWithString: "")
    private let statusLabel = NSTextField(labelWithString: "")
    private let cancelButton = NSButton(title: "Cancel", target: nil, action: nil)

    private var startedAt = Date()
    private var hasProgress = false

    init() {
        let panel = NSPanel(
            contentRect: NSRect(x: 0, y: 0, width: 360, height: 160),
            styleMask: [.titled, .closable, .nonactivatingPanel],
            backing: .buffered,
            defer: false
        )
        panel.center()
        panel.level = .floating
        panel.title = "Mosaic"
        panel.isMovableByWindowBackground = true
        panel.contentView = GradientBackgroundView()

        super.init(window: panel)
        setupUI()
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    private func setupUI() {
        guard let content = window?.contentView else { return }

        let lightBlue = NSColor(red: 41/255, green: 171/255, blue: 226/255, alpha: 1)
        let darkBlue  = NSColor(red: 39/255, green: 120/255, blue: 180/255, alpha: 1)

        filenameLabel.frame = NSRect(x: 20, y: 118, width: 320, height: 22)
        filenameLabel.font = NSFont.systemFont(ofSize: 13, weight: .semibold)
        filenameLabel.textColor = darkBlue
        filenameLabel.lineBreakMode = .byTruncatingMiddle

        progressBar.frame = NSRect(x: 20, y: 90, width: 320, height: 16)
        progressBar.minValue = 0
        progressBar.maxValue = 100
        progressBar.style = .bar
        progressBar.isIndeterminate = true
        progressBar.startAnimation(nil)

        statsLabel.frame = NSRect(x: 20, y: 66, width: 320, height: 18)
        statsLabel.font = NSFont.monospacedDigitSystemFont(ofSize: 11, weight: .regular)
        statsLabel.textColor = lightBlue

        statusLabel.frame = NSRect(x: 20, y: 44, width: 200, height: 18)
        statusLabel.font = NSFont.systemFont(ofSize: 11)
        statusLabel.textColor = .tertiaryLabelColor

        cancelButton.frame = NSRect(x: 260, y: 12, width: 80, height: 24)
        cancelButton.bezelStyle = .rounded
        cancelButton.target = self
        cancelButton.action = #selector(cancelTapped)

        content.addSubview(filenameLabel)
        content.addSubview(progressBar)
        content.addSubview(statsLabel)
        content.addSubview(statusLabel)
        content.addSubview(cancelButton)
    }

    func beginTracking(filename: String) {
        startedAt = Date()
        hasProgress = false

        filenameLabel.stringValue = filename
        statsLabel.stringValue = "Preparing…"
        statusLabel.stringValue = "Encoding shards"

        progressBar.isIndeterminate = true
        progressBar.startAnimation(nil)
    }

    func update(dispatched: Int, total: Int) {
        guard total > 0 else { return }

        let elapsed = Date().timeIntervalSince(startedAt)
        let percent = Double(dispatched) / Double(total) * 100

        if !hasProgress {
            hasProgress = true
            progressBar.stopAnimation(nil)
            progressBar.isIndeterminate = false
            statusLabel.stringValue = "Uploading…"
        }

        progressBar.doubleValue = percent
        statsLabel.stringValue = String(
            format: "%d / %d shards · %.1fs", dispatched, total, elapsed)
    }

    func finish() {
        progressBar.isIndeterminate = false
        progressBar.doubleValue = 100

        let elapsed = Date().timeIntervalSince(startedAt)
        statsLabel.stringValue = String(format: "Complete · %.1fs", elapsed)
        statusLabel.stringValue = "Upload complete"
        cancelButton.isEnabled = false

        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { [weak self] in
            self?.close()
        }
    }

    @objc private func cancelTapped() {
        DaemonClient.shared.cancelOp()
        close()
    }
}
