package cli

// to use:
// go build -o mos ./cmd/mosaic-node
// sudo mv mos /usr/local/bin/
import (
	// "flag"
	_ "embed" // required for //go:embed
	"fmt"
	"os"
)

//go:embed HelpMessage.txt
var helpMessage string
var args []string

func Run(Args []string) {
	args = Args
	if len(args) < 2 {
		fmt.Println("Usage: mos <command> [arguments]")
		os.Exit(1)
	}

	switch args[1] {
	case "join":
		switch args[2] {
		case "network":
			joinNetwork()
		default:
			fmt.Println("Unknown argument:", args[2])
			os.Exit(1)
		}
	case "status":
		if len(args) != 3 {
			fmt.Println("Please give a valid command.")
			os.Exit(1)
		}
		switch args[2] {
		case "network":
			statusNetwork()
		case "node":
			statusNode()
		case "account":
			statusAccount()
		default:
			fmt.Println("Unknown argument:", args[2])
			os.Exit(1)

		}
	case "login":
		if len(args) != 4 {
			fmt.Println("Usage: mos login key <key>")
			os.Exit(1)
		}
		switch args[2] {
		case "key":
			loginWithKey()
		default:
			fmt.Println("Unknown argument:", args[2])
			os.Exit(1)
		}
	case "logout":
		if len(args) != 3 {
			fmt.Println("Use mos logout account to logout.")
			os.Exit(1)
		}
		switch args[2] {
		case "account":
			logoutAccount()
		default:
			fmt.Println("Unknown argument:", args[2])
			os.Exit(1)
		}

	case "peers":
		if len(args) != 3 {
			fmt.Println("Use mos peers network to view peers.")
			os.Exit(1)
		}
		switch args[2] {
		case "network":
			peersNetwork()
		default:
			fmt.Println("Unknown argument:", args[2])
			os.Exit(1)
		}
	case "empty":
		if len(args) != 3 {
			fmt.Println("Use mos peers network to view peers.")
			os.Exit(1)
		}
		switch args[2] {
		case "storage":
			emptyStorage()
		default:
			fmt.Println("Unknown argument:", args[2])
			os.Exit(1)
		}
	case "leave":
		if len(args) != 3 {
			fmt.Println("Use mos leave network to leave the network.")
			os.Exit(1)
		}
		switch args[2] {
		case "network":
			leaveNetwork()
		default:
			fmt.Println("Unknown argument:", args[2])
			os.Exit(1)
		}
	case "list":
		if len(args) != 3 {
			fmt.Println("Use mos list file to view peers.")
			os.Exit(1)
		}
		switch args[2] {
		case "file":
			listFile()
		default:
			fmt.Println("Unknown argument:", args[2])
			os.Exit(1)
		}
	case "version":
		if len(args) != 2 {
			fmt.Println("Please give a valid command.")
			os.Exit(1)
		}
		version()
	case "help":
		help()
	default:
		fmt.Println("Unknown command:", args[1])
		os.Exit(1)
	}
}

func joinNetwork() {
	//storage, peers, err := joinNetworkMethod()
	storage := 0
	peers := 0
	var err error = nil
	exitOnErr(err, "Error joining network:")
	fmt.Println("Network joined successfully.")
	fmt.Printf("%d GB of storage shared\n", storage)
	fmt.Printf("Connected to %d peers\n", peers)
}

func statusNetwork() {
	//totalStor, freeStor, peers, err := statusNetwork()
	totalStor := 0
	freeStor := 0
	peers := 0
	var err error = nil
	exitOnErr(err, "Error getting network status:")
	fmt.Printf("Total Storage: %d\n", totalStor)
	fmt.Printf("Storage Available: %d\n", freeStor)
	fmt.Printf("Connected to %d peers\n", peers)
}
func statusNode() {
	if len(args) < 4 {
		fmt.Println("Usage: mos status node <node_id>")
		os.Exit(1)
	}
	nodeID := args[3]
	// username, id, storShared, storUsed, err := nodeStatus()
	username := "GavJoons"
	storShared := 68
	storUsed := 69
	var err error = nil
	exitOnErr(err, "Error getting node status:")
	fmt.Printf("Node ID: %s@node-%d\n", username, nodeID)
	fmt.Printf("Storage Shared: %d GB\n", storShared)
	fmt.Printf("Storage Used: %d GB\n", storUsed)
}
func statusAccount() {
	// []nodes, accountStor, availStor, usedStor, err := statusNetwork()
	nodes := [2]string{"Node 1", "Node 2"}
	accountStor := 100
	availStor := 88
	usedStor := 44
	var err error = nil
	exitOnErr(err, "Error getting account status:")
	fmt.Printf("Nodes: \n %v\n", nodes)
	fmt.Printf("Account storage shared: %d GB\n", accountStor)
	fmt.Printf("Network reserved: %d GB\n", accountStor-availStor)
	fmt.Printf("Available storage: %d GB\n", availStor)
	fmt.Printf("Used storage: %d GB\n", usedStor)
}
func loginWithKey() {
	key := args[3]
	//nodeID :=  createNode()
	nodeID := 67
	fmt.Printf("Logged in as: %s (login)\n", key)
	fmt.Printf("Logged in at: %s@node-%d\n", key, nodeID)
}
func logoutAccount() {
	// err := logout()
	var err error = nil
	exitOnErr(err, "Error logging out:")
	fmt.Println("Account logout successfull.")
}
func peersNetwork() {
	// []peers, err := getPeers()
	var err error = nil
	exitOnErr(err, "Error fetching peers:")
	type Peer struct {
		user string
		id   int
		data int
	}
	peers := []Peer{
		{"Gavin", 67, 15},
		{"Vihan", 68, 20},
	}
	for _, peer := range peers {
		fmt.Printf("%s@node-%d | Shared: %d GB\n", peer.user, peer.id, peer.data)
	}
}
func emptyStorage() {
	// data, err := emptyStorage()
	data := 12
	var err error = nil
	exitOnErr(err, "Error emptying storage:")
	fmt.Printf("%d GB of data deleted successfully.", data)
}
func leaveNetwork() {
	// err := leaveNetworkMethod()
	var err error = nil
	exitOnErr(err, "Error leaving network:")
	fmt.Printf("Network left successfully.")
}
func listFile() {
	// []files, err := listFiles()
	// the files should have metadata and stuff but just for concept ill use
	// an array of strings
	files := []string{"text.txt", "pic.jpg"}
	var err error = nil
	exitOnErr(err, "Error listing files:")
	for _, file := range files {
		fmt.Println(file)
	}

}
func version() {
	// vers := getVersion()
	vers := "1.2.26"
	var err error = nil
	exitOnErr(err, "Error emptying storage:")
	fmt.Printf("mos version %v\n", vers)

}
func help() {
	fmt.Println(helpMessage)
}
func exitOnErr(err error, msg string) {
	if err != nil {
		fmt.Println(msg, err)
		os.Exit(1)
	}
}
