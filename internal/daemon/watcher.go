package daemon

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
	"github.com/hcp-uw/mosaic/internal/daemon/handlers"
	filesystem "github.com/hcp-uw/mosaic/internal/fileSystem"
)

// disappearedEntry holds a manifest entry for a file that just vanished,
// plus a timer that commits the delete if no CREATE claims it in time.
type disappearedEntry struct {
	entry filesystem.ManifestEntry
	timer *time.Timer
}

// recentCreate is a CREATE that had no disappeared pair yet — kept briefly
// so a late-arriving RENAME/REMOVE can claim it (reversed-order events).
type recentCreate struct {
	logicalName string
	timer       *time.Timer
}

// DirWatcher maps ~/Mosaic/ filesystem events to network operations.
type DirWatcher struct {
	mosaicDir      string
	suppress       sync.Map // path → true: ignore next event for this path
	disappeared    sync.Map // logicalName → *disappearedEntry
	recentCreates  sync.Map // logicalName → *recentCreate: CREATEs waiting for a RENAME pair
}

// GlobalWatcher is set when the watcher starts so HTTP handlers can suppress events.
var GlobalWatcher *DirWatcher

// SuppressNext tells the watcher to ignore the next event for path.
// Call this before any daemon-initiated file operation in ~/Mosaic/.
func (w *DirWatcher) SuppressNext(path string) {
	w.suppress.Store(path, true)
	// Auto-expire after 500ms in case the event never fires.
	time.AfterFunc(500*time.Millisecond, func() {
		w.suppress.Delete(path)
	})
}

// StartDirWatcher begins watching mosaicDir and returns the watcher.
func StartDirWatcher(mosaicDir string) (*DirWatcher, error) {
	w := &DirWatcher{mosaicDir: mosaicDir}
	GlobalWatcher = w

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := fsw.Add(mosaicDir); err != nil {
		fsw.Close()
		return nil, err
	}

	go w.loop(fsw)
	fmt.Println("Dir watcher started on", mosaicDir)
	return w, nil
}

func (w *DirWatcher) loop(fsw *fsnotify.Watcher) {
	for {
		select {
		case event, ok := <-fsw.Events:
			if !ok {
				return
			}
			w.handleEvent(event)
		case err, ok := <-fsw.Errors:
			if !ok {
				return
			}
			fmt.Println("Watcher error:", err)
		}
	}
}

func (w *DirWatcher) handleEvent(event fsnotify.Event) {
	path := event.Name
	filename := filepath.Base(path)

	fmt.Printf("[watcher] raw event: op=%s path=%s\n", event.Op, path)

	// Ignore hidden files (manifest, temp files, etc.)
	if strings.HasPrefix(filename, ".") {
		fmt.Printf("[watcher] ignored hidden file: %s\n", filename)
		return
	}

	// Check suppress list — daemon-initiated operations register here first.
	if _, suppressed := w.suppress.LoadAndDelete(path); suppressed {
		fmt.Printf("[watcher] suppressed (daemon-initiated): %s\n", filename)
		return
	}

	// Derive the logical (network) name, stripping .mosaic suffix for stubs.
	logicalName, _ := strings.CutSuffix(filename, ".mosaic")

	inManifest := filesystem.IsInManifest(w.mosaicDir, logicalName)
	fmt.Printf("[watcher] handling %s | logical=%s | inManifest=%v\n", event.Op, logicalName, inManifest)

	switch {
	case event.Op&(fsnotify.Remove|fsnotify.Rename) != 0:
		w.onDisappeared(logicalName)

	case event.Op&fsnotify.Create != 0:
		w.onCreate(logicalName)
	}
}

// onDisappeared handles both REMOVE and RENAME (macOS uses either for renames).
// First checks if a CREATE already arrived (reversed-order events). If so, it's
// a rename. Otherwise saves the entry and waits 500ms for a late CREATE.
func (w *DirWatcher) onDisappeared(logicalName string) {
	if !filesystem.IsInManifest(w.mosaicDir, logicalName) {
		fmt.Printf("[watcher] disappeared ignored — %s not in manifest\n", logicalName)
		return
	}

	// Snapshot the manifest entry before anything removes it.
	entries, err := filesystem.ReadManifest(w.mosaicDir)
	if err != nil {
		fmt.Printf("[watcher] could not read manifest for snapshot: %v\n", err)
		return
	}
	entry, ok := entries[logicalName]
	if !ok {
		return
	}

	// If cached=false the daemon already marked this file as uncached before
	// removing the local copy (deleteStub, logout). Don't treat it as a
	// user-initiated network delete.
	if !entry.Cached {
		fmt.Printf("[watcher] skipping delete for %s — already marked uncached\n", logicalName)
		return
	}

	// Check if a CREATE already arrived for a different name (CREATE before RENAME).
	var matchedNew string
	var matchedRC *recentCreate
	w.recentCreates.Range(func(key, val any) bool {
		name := key.(string)
		if name != logicalName {
			matchedNew = name
			matchedRC = val.(*recentCreate)
			return false
		}
		return true
	})

	if matchedNew != "" {
		matchedRC.timer.Stop()
		w.recentCreates.Delete(matchedNew)
		fmt.Printf("[watcher] RENAME paired (CREATE first): %s → %s\n", logicalName, matchedNew)
		w.renameOnNetwork(entry, matchedNew)
		return
	}

	// No CREATE yet — park the entry and wait.
	// Cancel any existing timer for this name (duplicate events).
	if existing, loaded := w.disappeared.Load(logicalName); loaded {
		existing.(*disappearedEntry).timer.Stop()
	}

	fmt.Printf("[watcher] %s disappeared — waiting 500ms for CREATE pair\n", logicalName)

	d := &disappearedEntry{entry: entry}
	d.timer = time.AfterFunc(500*time.Millisecond, func() {
		if _, stillPending := w.disappeared.LoadAndDelete(logicalName); stillPending {
			fmt.Printf("[watcher] no CREATE arrived — committing delete for %s\n", logicalName)
			w.deleteFromNetwork(entry)
		}
	})
	w.disappeared.Store(logicalName, d)
}

// onCreate handles a CREATE event. Four cases:
//  1. Same name as a disappeared entry → Ctrl+Z undo, restore it
//  2. Different name from a disappeared entry → rename, update network
//  3. No disappeared entry yet — park it as a recentCreate for 500ms in case
//     the RENAME arrives late (macOS sometimes fires CREATE before RENAME)
func (w *DirWatcher) onCreate(newLogicalName string) {
	// Case 1: same name — Ctrl+Z undo.
	if val, ok := w.disappeared.LoadAndDelete(newLogicalName); ok {
		d := val.(*disappearedEntry)
		d.timer.Stop()
		fmt.Printf("[watcher] UNDO detected — restoring %s to manifest\n", newLogicalName)
		w.restoreEntry(d.entry)
		return
	}

	// Case 2: different name — rename (RENAME arrived before CREATE, normal order).
	var matchedOld string
	var matchedEntry *disappearedEntry

	w.disappeared.Range(func(key, val any) bool {
		oldName := key.(string)
		if oldName != newLogicalName {
			matchedOld = oldName
			matchedEntry = val.(*disappearedEntry)
			return false
		}
		return true
	})

	if matchedOld != "" {
		matchedEntry.timer.Stop()
		w.disappeared.Delete(matchedOld)
		fmt.Printf("[watcher] RENAME paired (RENAME first): %s → %s\n", matchedOld, newLogicalName)
		w.renameOnNetwork(matchedEntry.entry, newLogicalName)
		return
	}

	// Case 3: no disappeared entry yet — park this CREATE briefly.
	// If a RENAME arrives within 500ms claiming a manifest file, this is the new name.
	// Otherwise treat it as a drag-and-drop upload after the window expires.
	fmt.Printf("[watcher] CREATE parked — %s waiting 500ms for late RENAME\n", newLogicalName)
	rc := &recentCreate{logicalName: newLogicalName}
	rc.timer = time.AfterFunc(500*time.Millisecond, func() {
		w.recentCreates.Delete(newLogicalName)

		// Skip .mosaic stubs — those are written by the daemon itself.
		if strings.HasSuffix(newLogicalName, ".mosaic") {
			return
		}

		// If the file is still not in the manifest, it was dragged in by the user.
		if !filesystem.IsInManifest(w.mosaicDir, newLogicalName) {
			filePath := filepath.Join(w.mosaicDir, newLogicalName)
			fmt.Printf("[watcher] drag-and-drop detected — ingesting %s\n", newLogicalName)
			handlers.IngestLocalFile(filePath)
		}
	})
	w.recentCreates.Store(newLogicalName, rc)
}

// deleteFromNetwork commits a delete to the network after the timer fires.
// handlers.DeleteFile handles all cleanup: stub, real file, local manifest,
// network manifest update, and P2P broadcast.
func (w *DirWatcher) deleteFromNetwork(entry filesystem.ManifestEntry) {
	logicalName := entry.Name

	// Suppress any watcher events the handler will generate (stub removal, etc.)
	// so we don't re-interpret them as user actions.
	w.SuppressNext(filepath.Join(w.mosaicDir, logicalName))
	w.SuppressNext(filepath.Join(w.mosaicDir, logicalName+".mosaic"))

	// TODO: trigger real network shard deletion here.
	handlers.DeleteFile(protocol.DeleteFileRequest{FilePath: logicalName})
	fmt.Printf("[watcher] deleted %s from network and manifest\n", logicalName)
}

// restoreEntry re-adds a manifest entry after a Ctrl+Z undo.
func (w *DirWatcher) restoreEntry(entry filesystem.ManifestEntry) {
	if err := filesystem.RestoreManifestEntry(w.mosaicDir, entry); err != nil {
		fmt.Printf("[watcher] failed to restore manifest entry for %s: %v\n", entry.Name, err)
		return
	}
	fmt.Printf("[watcher] restored %s to manifest\n", entry.Name)
	// TODO: trigger real network restore here.
}

// renameOnNetwork delegates to the RenameFile handler, which handles all local
// state (stub, real file, manifest) and will propagate to the network.
func (w *DirWatcher) renameOnNetwork(oldEntry filesystem.ManifestEntry, newName string) {
	oldName := oldEntry.Name

	// Suppress the filesystem events that the handler will generate so the
	// watcher doesn't re-interpret them as user actions.
	w.SuppressNext(filepath.Join(w.mosaicDir, oldName))
	w.SuppressNext(filepath.Join(w.mosaicDir, oldName+".mosaic"))
	w.SuppressNext(filepath.Join(w.mosaicDir, newName))
	w.SuppressNext(filepath.Join(w.mosaicDir, newName+".mosaic"))

	resp := handlers.RenameFile(protocol.RenameFileRequest{FilePath: oldName, NewName: newName})
	if resp.Success {
		fmt.Printf("[watcher] rename complete: %s → %s\n", oldName, newName)
	} else {
		fmt.Printf("[watcher] rename failed: %s\n", resp.Details)
	}
}
