All OS are theoretically supported however as of 01/28/2026 non-MacOS has not been tested yet.

Module 1 is the user experience way to startup and shutdown mosaic.
Module 2 is dev testing stuff

## Module 1:
While in mosaic:
./scripts/install.sh

To shutdown:
mos shutdown

## Module 2:

While in mosaic directory

Developer tools:
- ./install.sh -d -> extra debugging info including PID

To kill the background process and clean up run:
- pkill mosaicd && rm -f /tmp/mosaicd.sock /tmp/mosaicd.pid /tmp/mosaicd.log

