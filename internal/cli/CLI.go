package cli

// to use:
// go build -o mos ./cmd/mosaic-node
// sudo mv mos /usr/local/bin/
import (
	// "flag"
	_ "embed" // required for //go:embed
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	case "version":
		if len(args) != 2 {
			fmt.Println("Please give a valid command.")
			os.Exit(1)
		}
		version()
	case "join":
		switch args[2] {
		case "network":
			joinNetwork()
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
	case "status":
		if len(args) != 3 || len(args) != 4 {
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
	case "set":
		if len(args) != 4 {
			fmt.Println("Use mos set storage <amount> to set storage.")
			os.Exit(1)
		}
		switch args[2] {
		case "storage":
			setStorage()
		default:
			fmt.Println("Unknown argument:", args[2])
			os.Exit(1)
		}
	case "empty":
		if len(args) != 3 {
			fmt.Println("Use mos empty storage to delete all stored data in the network.")
			os.Exit(1)
		}
		switch args[2] {
		case "storage":
			emptyStorage()
		default:
			fmt.Println("Unknown argument:", args[2])
			os.Exit(1)
		}
	case "list":
		if len(args) != 3 {
			fmt.Println("Use mos list file to list all files on the network.")
			os.Exit(1)
		}
		switch args[2] {
		case "file":
			listFile()
		default:
			fmt.Println("Unknown argument:", args[2])
			os.Exit(1)
		}
	case "upload":
		if len(args) != 4 {
			fmt.Println("Usage:")
			fmt.Println("mos upload file <path>")
			fmt.Println("mos upload folder <path>")
			os.Exit(1)
		}
		switch args[2] {
		case "file":
			uploadFile()
		case "folder":
			uploadFolder()
		default:
			fmt.Println("Unknown argument:", args[2])
			os.Exit(1)
		}
	case "download":
		if len(args) != 4 {
			fmt.Println("Usage:")
			fmt.Println("mos download file <path>")
			fmt.Println("mos download folder <path>")
			os.Exit(1)
		}
		switch args[2] {
		case "file":
			downloadFile()
		case "folder":
			downloadFolder()
		default:
			fmt.Println("Unknown argument:", args[2])
			os.Exit(1)
		}
	case "info":
		if len(args) != 4 {
			fmt.Println("Usage:")
			fmt.Println("mos info file <path>")
			fmt.Println("mos info folder <path>")
			os.Exit(1)
		}
		switch args[2] {
		case "file":
			fileInfo()
		case "folder":
			folderInfo()
		default:
			fmt.Println("Unknown argument:", args[2])
			os.Exit(1)
		}
	case "delete":
		if len(args) != 4 {
			fmt.Println("Usage:")
			fmt.Println("mos delete file <path>")
			fmt.Println("mos delete folder <path>")
			os.Exit(1)
		}
		switch args[2] {
		case "file":
			deleteFile()
		case "folder":
			deleteFolder()
		default:
			fmt.Println("Unknown argument:", args[2])
			os.Exit(1)
		}
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
	fmt.Printf("Node ID: %s@node-%v\n", username, nodeID)
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
func setStorage() {
	amountStr := args[3]
	amount, err := strconv.Atoi(amountStr)
	if err != nil {
		fmt.Println("Please enter a valid integer amount (in GB).")
		os.Exit(1)
	}
	total := GetTotalStorage()
	if amount > total {
		fmt.Printf("Error: cannot set storage to %d GB, exceeds total capacity of %d GB.\n", amount, total)
		os.Exit(1)
	}

	// storUsable, err := setStorageMethod(amount)
	storUsable := 80
	var errStor error = nil
	exitOnErr(errStor, "Error setting storage: ")
	fmt.Printf("Storage successfully set to %d GB.\nUsable storage: %d GB.\n", amount, storUsable)
}
func emptyStorage() {
	// data, err := emptyStorage()
	data := 12
	var err error = nil
	exitOnErr(err, "Error emptying storage:")
	fmt.Printf("%d GB of data deleted successfully.\n", data)
}
func leaveNetwork() {
	// err := leaveNetworkMethod()
	var err error = nil
	exitOnErr(err, "Error leaving network:")
	fmt.Println("Network left successfully.")
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
func uploadFile() {
	filePath := args[3]

	fileInfo, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		fmt.Println("Error: file does not exist at path:", filePath)
		os.Exit(1)
	}
	exitOnErr(err, "Error reading file info:")

	if fileInfo.IsDir() {
		fmt.Println("Your provided path points to a directory. Use 'mos upload folder <path>' instead.")
		os.Exit(1)
	}

	// uploadErr := uploadFile(filePath)
	var uploadErr error = nil
	exitOnErr(uploadErr, "Error uploading file.")
	fileSize := fileInfo.Size() / 1024
	fmt.Printf("Uploading file: %s (%d KB)\n", fileInfo.Name(), fileSize)
	fmt.Printf("File '%s' uploaded successfully to network.\n", fileInfo.Name())
	storage := GetTotalStorage()
	fmt.Printf("Storage remaining: %d GB\n", storage)
}
func uploadFolder() {
	root := args[3]

	info, err := os.Stat(root)
	if os.IsNotExist(err) {
		fmt.Println("Error: folder does not exist at path:", root)
		os.Exit(1)
	}
	exitOnErr(err, "Error reading folder info:")

	if !info.IsDir() {
		fmt.Println("Error: path points to a file. Use 'mos upload file <path>' instead.")
		os.Exit(1)
	}

	fmt.Printf("Starting upload of folder: %s\n", root)
	err = UploadFolderRecursive(root, false, true)
	exitOnErr(err, "Error uploading folder:")
	if root == "." {
		fmt.Println("Finished uploading current folder.")
	} else {
		fmt.Printf("Finished uploading folder: %s\n", root)
	}
	storage := GetTotalStorage()
	fmt.Printf("Storage remaining: %d GB\n", storage)
}
func UploadFolderRecursive(path string, showSubFiles bool, isRoot bool) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		fullPath := filepath.Join(path, entry.Name())

		if entry.IsDir() {
			fmt.Println("Uploading folder:", entry.Name())
			err := UploadFolderRecursive(fullPath, showSubFiles, false)
			if err != nil {
				return err
			}
		} else {
			// replace with: uploadErr := uploadFile(fullPath)
			var uploadErr error = nil
			if uploadErr != nil {
				return uploadErr
			}
			if isRoot || showSubFiles {
				fmt.Println("Uploading file:", entry.Name())
			}
		}
	}

	return nil
}
func downloadFile() {
	filePath := args[3]
	// err := downloadFileMethod(filePath)
	var err error = nil
	exitOnErr(err, "Error downloading "+filePath+": ")
	fmt.Printf("File '%v' downloaded successfully from network.\n", filePath)
}
func downloadFolder() {
	folderPath := args[3]
	// err := downloadFolderMethod(folderPath)
	var err error = nil
	exitOnErr(err, "Error downloading "+folderPath+": ")
	fmt.Printf("Folder '%v' downloaded successfully from network.\n", folderPath)
}
func deleteFile() {
	// since the deleting wont be local I cant really do much for it here
	filePath := args[3]
	// err := deleteFileMethod(filePath)
	var err error = nil
	exitOnErr(err, "Error deleting "+filePath+": ")
	fmt.Printf("File '%v' deleted successfully from network.\n", filePath)
	storage := GetTotalStorage()
	fmt.Printf("Storage remaining: %d GB\n", storage)
}
func deleteFolder() {
	// since the deleting wont be local I cant really do much for it here
	folderPath := args[3]
	// err := deleteFolderMethod(folderPath)
	var err error = nil
	exitOnErr(err, "Error deleting "+folderPath+": ")
	fmt.Printf("Folder '%v' deleted successfully from network.\n", folderPath)
	storage := GetTotalStorage()
	fmt.Printf("Storage remaining: %d GB\n", storage)
}
func fileInfo() {
	// idk what metadata we want exactly but this should be a rough estimate to serve as a model
	type File struct {
		name      string
		nodeID    int
		dateAdded string
		size      int
	}
	// info, err := getFileInfo(args[3])
	var err error = nil
	exitOnErr(err, "Error getting info on file.")
	info := File{"image.jpg", 67, "06-07-2025", 20}
	fmt.Printf(
		"Name: %s\n"+
			"	NodeID: %d\n"+
			"	Date Added: %v\n"+
			"	Size: %d GB\n",
		info.name, info.nodeID, info.dateAdded, info.size,
	)
}
func folderInfo() {
	// idk what metadata we want exactly but this should be a rough estimate to serve as a model
	type Folder struct {
		name      string
		nodeID    int
		dateAdded string
		size      int
		numFiles  int
	}
	// info, err := getFolderInfo(args[3])
	var err error = nil
	exitOnErr(err, "Error getting info on folder.")
	info := Folder{"FriesInBag", 67, "06-08-2025", 50, 23}
	fmt.Printf(
		"Name: %s\n"+
			"	NodeID: %d\n"+
			"	Date Added: %v\n"+
			"	Size: %d GB\n"+
			"	Number of files: %d",
		info.name, info.nodeID, info.dateAdded, info.size, info.numFiles,
	)
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
func GetTotalStorage() int {
	// also add a method to check how much storage is left and print that out
	// storage, err := getStorage()
	storage := 67
	var errS error = nil
	exitOnErr(errS, "Error fetching storage.")
	return storage
}
func exitOnErr(err error, msg string) {
	if err != nil {
		fmt.Println(msg, err)
		os.Exit(1)
	}
}
