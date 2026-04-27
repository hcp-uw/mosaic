import Cocoa

// If another instance of Mosaic is already running, activate it and quit.
// This ensures double-clicking a stub routes to the existing instance.
let bundleID = Bundle.main.bundleIdentifier ?? "com.mosaic.Mosaic"
let running = NSRunningApplication.runningApplications(withBundleIdentifier: bundleID)
    .filter { $0.processIdentifier != ProcessInfo.processInfo.processIdentifier }

if !running.isEmpty {
    running.first?.activate(options: .activateIgnoringOtherApps)
    exit(0)
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.run()
