// Command client connects to a relay and exchanges messages with other clients.
//
// It prints anything the relay forwards to it. In interactive mode it also
// sends each line typed on stdin. For scripted testing, pass -msg to send a
// single message and -wait to listen for incoming messages for a fixed
// duration before exiting.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"time"
)

func main() {
	relay := flag.String("relay", "127.0.0.1:9000", "relay address host:port")
	msg := flag.String("msg", "", "optional message to send on connect")
	wait := flag.Duration("wait", 0, "if >0, listen this long then exit instead of reading stdin")
	flag.Parse()

	conn, err := net.Dial("tcp", *relay)
	if err != nil {
		log.Fatalf("client: dial %s: %v", *relay, err)
	}
	defer conn.Close()

	log.Printf("client: connected to relay %s", *relay)

	// Print everything the relay forwards to us.
	go func() {
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			fmt.Println(sc.Text())
		}
	}()

	if *msg != "" {
		if _, err := fmt.Fprintf(conn, "%s\n", *msg); err != nil {
			log.Fatalf("client: send: %v", err)
		}
		log.Printf("client: sent: %s", *msg)
	}

	if *wait > 0 {
		time.Sleep(*wait)
		log.Printf("client: done listening, exiting")
		return
	}

	// Interactive mode: forward each stdin line to the relay.
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		if _, err := fmt.Fprintf(conn, "%s\n", sc.Text()); err != nil {
			log.Fatalf("client: send: %v", err)
		}
	}
}
