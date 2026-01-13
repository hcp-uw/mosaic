Only macOS is supported for now.

Version 1:

When in the mosaic directory, run:
- go run ./cmd/mosaic-node &
to start the mosaic background process. 

To start mosaic, while in the mosaic directory run:
- go build -o mos ./cmd/mosaic-cli
- go build -o mosaicd ./cmd/mosaic-node
- sudo mv mos /usr/local/bin/
- sudo mv mosaicd /usr/local/bin/


Start the Daemon:
- mosaicd > /tmp/mosaicd.log 2>&1 &

Overall copy and paste version to start mosaic:

- go build -o mos ./cmd/mosaic-cli; go build -o mosaicd ./cmd/mosaic-node; sudo mv mos /usr/local/bin/; sudo mv mosaicd /usr/local/bin/; mosaicd > /tmp/mosaicd.log 2>&1 &

Mosaic should now work as needed!

To kill the background process and clean up run:
- pkill mosaicd && rm -f /tmp/mosaicd.sock /tmp/mosaicd.pid /tmp/mosaicd.log

Version 2:
While in mosaic:
./install.sh

To shutdown:
mos shutdown

Version 3:

Or run:
- make build
- make install
- make start

To end:
- make stop
- make uninstall
- make clean

Or even easier, to overall start run:
- make quickstart

To end run:
- make shutdown
