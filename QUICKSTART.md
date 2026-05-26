# Mosaic Quickstart (macOS)

This guide is for trying Mosaic quickly on your laptop, assuming the remote
infrastructure is already running:

- relay on node1: `45.32.226.71:9000`
- storage nodes on node1 + node2

## 1. Clone

```sh
git clone https://github.com/hcp-uw/mosaic.git
cd mosaic
git checkout working_demo
```

## 2. One-command local install (recommended)

This will:
- build the client
- set your key
- install `Mosaic.app` for double-click rehydrate
- start a background watcher (`-node`) via LaunchAgent

```sh
deploy/install-local.sh "<your-secret>" 45.32.226.71:9000
```

Notes:
- This key defines your identity namespace.
- Use the same key on any other device where you want to rehydrate your files.

## 3. Confirm watcher is running

```sh
launchctl print "gui/$(id -u)/io.mosaic.watcher" | head
```

No foreground terminal is required with this installer.

## 4. Try it with a file

In another terminal:

```sh
mkdir -p ~/Mosaic
cp /path/to/your/file ~/Mosaic/
```

After a moment, you should see:
- `~/Mosaic/file.mosaic`
- original `~/Mosaic/file` removed

## 5. Rehydrate

CLI:

```sh
go run ./client -relay 45.32.226.71:9000 -rehydrate ~/Mosaic/file.mosaic -open
```

Or just double-click `file.mosaic` in Finder if you installed `Mosaic.app`.

## 6. Quick sync test (optional)

Delete the stub:

```sh
rm -f ~/Mosaic/file.mosaic
```

Within about 1 minute, it should reappear automatically.

## Troubleshooting

- `missing ~/Mosaic/.mosaic-key`
  - Run step 2 again.

- Stub does not appear after dropping a file
  - Make sure LaunchAgent watcher is running:
    - `launchctl print "gui/$(id -u)/io.mosaic.watcher" | head`
  - Ensure at least one remote node is connected.

- Rehydrate fails
  - Confirm you are using the same key that was used when the file was sharded.
  - Confirm relay address is `45.32.226.71:9000`.

- macOS does not use Mosaic app on double-click
  - Right-click stub -> `Open With` -> `Mosaic` -> `Always Open With`.

## Optional: uninstall

```sh
deploy/uninstall-local.sh
```

Remove key too:

```sh
deploy/uninstall-local.sh --remove-key
```
