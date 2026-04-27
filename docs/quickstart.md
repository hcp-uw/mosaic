All OS are theoretically supported however as of 01/28/2026 non-MacOS has not been tested yet.

Module 1 is the user experience way to startup and shutdown mosaic.
Module 2 is for developer testing mosaic
Module 3 is the actual code being run (for informational/dev reasons)

## Module 1:
While in mosaic:
./install.sh

To shutdown:
mos shutdown

## Module 2:

While in mosaic directory

To start run:
- make build -> builds the executables
- make install -> moves executables
- make start -> starts background processes

To end:
- make stop -> stop daemon
- make uninstall -> remove executables
- make clean -> remove temp files

Or even easier, to overall start run:
- make quickstart

To end run:
- make shutdown

Developer tools:
- ./install.sh -d -> extra debugging info including PID
- make status -> gives the status of the Daemon and Socket
- make restart -> quickest way to restart mosaic to update your computer with newest changes

## Module 3:

To start mosaic...
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

