package cli

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/hcp-uw/mosaic/internal/cli/client"
	"github.com/hcp-uw/mosaic/internal/cli/handlers/helpers"
	"github.com/hcp-uw/mosaic/internal/cli/protocol"
)

//go:embed HelpMessage.txt
var helpMessage string
var args []string

func Run(Args []string) {
	args = Args
	if len(args) < 2 {
		fmt.Println()
		fmt.Println("Usage: mos <command> [arguments]")
		os.Exit(1)
	}

	switch args[1] {
	case "login":
		if len(args) != 4 {
			fmt.Println()
			fmt.Println("Usage:")
			fmt.Println("- mos login key <key>    Login with a key.")
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
			fmt.Println()
			fmt.Println("Usage:")
			fmt.Println("- mos logout account    Logout from the account.")
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
			fmt.Println()
			fmt.Println("Usage:")
			fmt.Println("- mos version    Get the current version.")
			os.Exit(1)
		}
		version()
	case "join":
		if len(args) != 3 {
			fmt.Println()
			fmt.Println("Usage:")
			fmt.Println("- mos join network    Join the network.")
			os.Exit(1)
		}
		switch args[2] {
		case "network":
			joinNetwork()
		default:
			fmt.Println("Unknown argument:", args[2])
			os.Exit(1)
		}
	case "leave":
		if len(args) != 3 {
			fmt.Println()
			fmt.Println("Usage:")
			fmt.Println("- mos leave network    Leave the network.")
			fmt.Println()
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
		if len(args) != 3 && len(args) != 4 {
			fmt.Println("Please give a valid command.")
			os.Exit(1)
		}
		switch args[2] {
		case "network":
			if len(args) != 3 {
				fmt.Println("Usage:")
				fmt.Println("- mos status network    View network status.")
				os.Exit(1)
			}
			statusNetwork()
		case "node":
			if len(args) != 4 {
				fmt.Println("Usage:")
				fmt.Println("- mos status node <node_id>    View node status.")
				os.Exit(1)
			}
			statusNode()
		case "account":
			if len(args) != 3 {
				fmt.Println("Usage:")
				fmt.Println("- mos status account    View account status.")
				os.Exit(1)
			}
			statusAccount()
		default:
			fmt.Println("Unknown argument:", args[2])
			os.Exit(1)
		}
	case "peers":
		if len(args) != 3 {
			fmt.Println("Usage:")
			fmt.Println("- mos peers network    View peers.")
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
			fmt.Println("Usage:")
			fmt.Println("- mos set storage <amount>    Set storage.")
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
			fmt.Println("Usage:")
			fmt.Println("- mos empty storage    Delete all stored data in the network.")
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
			fmt.Println("Usage:")
			fmt.Println("- mos list file    List all files on the network.")
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
			fmt.Println("- mos upload file <path>    Upload a file.")
			fmt.Println("- mos upload folder <path>  Upload a folder.")
			os.Exit(1)
		}
		switch args[2] {
		// uploadFile and uploadFolder take care of checking if path is file or folder
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
			fmt.Println("- mos download file <path>    Download a file.")
			fmt.Println("- mos download folder <path>  Download a folder.")
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
			fmt.Println("- mos info file <path>    Get info about a file.")
			fmt.Println("- mos info folder <path>  Get info about a folder.")
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
			fmt.Println("- mos delete file <path>    Delete a file.")
			fmt.Println("- mos delete folder <path>  Delete a folder.")
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
	case "shutdown":
		if len(args) != 2 {
			fmt.Println("Please give a valid command.")
			os.Exit(1)
		}
		Shutdown()
	case "help":
		help()
	default:
		fmt.Printf("mos: '%v' is not a mos command. See 'mos help'.\n", args[1])
		os.Exit(1)
	}
}

// Connects the user to the mosaic network
func joinNetwork() {
	resp, err := client.SendRequest("joinNetwork", protocol.JoinRequest{})
	exitOnErr(err, "Error joining network.")

	var cmdResp protocol.JoinResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}
	message := fmt.Sprintf("\nJoined network successfully.\n- Connected to %d peers.\n", cmdResp.Peers)
	fmt.Println(message)
}

// Gets overall network status
func statusNetwork() {
	resp, err := client.SendRequest("statusNetwork", protocol.NetworkStatusRequest{})
	exitOnErr(err, "Error getting network status.")

	var cmdResp protocol.NetworkStatusResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}
	message := fmt.Sprintf("\nNetwork Status:\n- Total Network Storage: %d GB\n- Your Available Storage: %d GB\n- Number of Peers: %d\n",
		cmdResp.NetworkStorage, cmdResp.AvailableStorage, cmdResp.Peers)
	fmt.Println(message)
}

// Gets info about a specific node in the network
func statusNode() {
	nodeID := args[3]
	resp, err := client.SendRequest("statusNode", protocol.NodeStatusRequest{ID: nodeID})
	exitOnErr(err, "Error getting node status.")

	var cmdResp protocol.NodeStatusResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}
	message := fmt.Sprintf("\nNode status processed successfully.\n- Node ID: %s@node-%v\n- Storage Shared: %d GB\n", cmdResp.Username, cmdResp.ID, cmdResp.StorageShare)
	fmt.Println(message)
}

// Gets info about the current account
func statusAccount() {
	resp, err := client.SendRequest("statusAccount", protocol.StatusAccountRequest{AccountID: helpers.GetAccountID()})
	exitOnErr(err, "Error getting account status.")

	var cmdResp protocol.StatusAccountResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}
	message := fmt.Sprintf("\nAccount status processed successfully.\n- Username: %s\n- Nodes: %v\n- Given Storage: %d GB\n"+
		"- Network Reserved Storage: %d GB\n- Available Storage: %d GB\n- Used Storage: %d GB\n", cmdResp.Username, cmdResp.Nodes, cmdResp.GivenStorage, cmdResp.GivenStorage-cmdResp.AvailableStorage, cmdResp.AvailableStorage, cmdResp.UsedStorage)
	fmt.Println(message)
}

// Logs in with a provided key
func loginWithKey() {
	key := args[3]
	resp, err := client.SendRequest("loginKey", protocol.LoginKeyRequest{Key: key})
	exitOnErr(err, "Error logging in with key.")

	var cmdResp protocol.LoginKeyResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}
	message := fmt.Sprintf("\nLogged in with key successfully.\n- Current Node: %s@node-%v\n", cmdResp.Username, cmdResp.CurrentNode)
	fmt.Println(message)
}

// Logs out of the current account
func logoutAccount() {
	resp, err := client.SendRequest("logout", protocol.LogoutRequest{AccountID: helpers.GetAccountID()})
	exitOnErr(err, "Error logging out.")

	var cmdResp protocol.LogoutResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}
	message := fmt.Sprintf("\nLogged out successfully.\n- Username: %s\n", cmdResp.Username)
	fmt.Println(message)
}

// List some data on all the peers in the network
func peersNetwork() {
	resp, err := client.SendRequest("getPeers", protocol.GetPeersRequest{})
	exitOnErr(err, "Error fetching peers.")

	var cmdResp protocol.GetPeersResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}

	message := "\nPeers in Network:\n"
	for _, peer := range cmdResp.Peers {
		message += fmt.Sprintf("- %s@node-%d | Shared: %d GB\n", peer.Username, peer.NodeID, peer.StorageShared)
	}
	fmt.Println(message)
}

// Sets the amount of storage allocated to the current node
func setStorage() {
	amountStr := args[3]
	amount, err := strconv.Atoi(amountStr)
	if err != nil {
		fmt.Println("Please enter a valid integer amount (in GB).")
		os.Exit(1)
	}
	total := helpers.UserStorageUsed()
	if amount > total {
		fmt.Printf("Error: cannot set storage to %d GB, exceeds total capacity of %d GB.\n", amount, total)
		os.Exit(1)
	}

	resp, err := client.SendRequest("setStorage", protocol.SetStorageRequest{Amount: amount, Node: helpers.GetNodeID()})
	exitOnErr(err, "Error setting storage.")

	var cmdResp protocol.SetStorageResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}
	message := fmt.Sprintf("\nStorage set successfully.\n- Current Node: %v@node-%d\n- Node Storage: %d GB\n- Available Storage: %d GB\n",
		cmdResp.Username, cmdResp.CurrentNode, cmdResp.NodeStorage, cmdResp.AvailableStorage)
	fmt.Println(message)
}

// Empties all storage allocated by the user in the network (deletes all their data from the network)
func emptyStorage() {
	resp, err := client.SendRequest("emptyStorage", protocol.EmptyStorageRequest{AccountID: helpers.GetAccountID()})
	exitOnErr(err, "Error emptying storage.")

	var cmdResp protocol.EmptyStorageResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}
	message := fmt.Sprintf("\nStorage emptied successfully.\n- Username: %s\n- Storage Deleted: %d GB\n- Available Storage: %d GB\n",
		cmdResp.Username, cmdResp.StorageDeleted, cmdResp.AvailableStorage)
	fmt.Println(message)
}

// The user leaves the network, deleting all their data from the network as well?
func leaveNetwork() {
	resp, err := client.SendRequest("leaveNetwork", protocol.LeaveNetworkRequest{AccountID: helpers.GetAccountID()})
	exitOnErr(err, "Error leaving network.")

	var cmdResp protocol.LeaveNetworkResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}
	message := fmt.Sprintf("\nNetwork left successfully.\n- Username: %s\n", cmdResp.Username)
	fmt.Println(message)
}

// Lists all files on the network
func listFile() {
	// the files should have metadata and stuff but just for concept ill use
	// an array of strings
	resp, err := client.SendRequest("listFiles", protocol.ListFilesRequest{AccountID: helpers.GetAccountID()})
	exitOnErr(err, "Error listing files.")

	var cmdResp protocol.ListFilesResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}
	message := "\nFiles on Network:\n"
	for _, file := range cmdResp.Files {
		message += fmt.Sprintf("- %s\n", file)
	}
	fmt.Println(message)
}

// Uploads a file to the network
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

	fileSize := fileInfo.Size() / 1024
	fmt.Printf("Uploading file: %s (%d KB)\n", fileInfo.Name(), fileSize)
	resp, uploadErr := client.SendRequest("uploadFile", protocol.UploadFileRequest{
		Path: filePath,
	})
	exitOnErr(uploadErr, "Error uploading file.")

	var cmdResp protocol.UploadFileResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}
	message := fmt.Sprintf("\nFile '%v' uploaded successfully to network.\n- Available storage remaining: %d GB.\n",
		cmdResp.FileName, cmdResp.AvailableStorage)
	fmt.Println(message)
}

// Uploads a folder to the network
func uploadFolder() {
	folderPath := args[3]
	currentFolder := false
	if folderPath == "." || folderPath == "./" {
		currentFolder = true
	}

	info, err := os.Stat(folderPath)
	if os.IsNotExist(err) {
		fmt.Println("Error: folder does not exist at path:", folderPath)
		os.Exit(1)
	}
	exitOnErr(err, "Error reading folder info:")

	if !info.IsDir() {
		fmt.Println("Your provided path points to a file. Use 'mos upload file <path>' instead.")
		os.Exit(1)
	}

	resp, err := client.SendRequest("uploadFolder", protocol.UploadFolderRequest{FolderPath: folderPath})
	exitOnErr(err, "Error uploading folder.")

	var cmdResp protocol.UploadFolderResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}

	var message string
	if currentFolder {
		message = fmt.Sprintf("\nCurrent folder uploaded successfully to network.\n- Available storage remaining: %v GB.\n",
			cmdResp.AvailableStorage)
	} else {
		message = fmt.Sprintf("\nFolder '%v' uploaded successfully to network.\n- Available storage remaining: %v GB.\n",
			cmdResp.FolderName, cmdResp.AvailableStorage)
	}

	fmt.Println(message)

}

// Downloads a file from the network
func downloadFile() {
	filePath := args[3]
	resp, err := client.SendRequest("downloadFile", protocol.DownloadFileRequest{FilePath: filePath})
	exitOnErr(err, "Error downloading "+filePath+": ")

	var cmdResp protocol.DownloadFileResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}
	message := fmt.Sprintf("\nFile '%v' downloaded successfully from network.\n"+
		"- Storage Remaining: %d GB\n", cmdResp.FileName, cmdResp.AvailableStorage)
	fmt.Println(message)
}

// Downloads a folder from the network
func downloadFolder() {
	filePath := args[3]
	resp, err := client.SendRequest("downloadFolder", protocol.DownloadFolderRequest{FolderPath: filePath})
	exitOnErr(err, "Error downloading "+filePath+": ")

	var cmdResp protocol.DownloadFolderResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}
	message := fmt.Sprintf("\nFolder '%v' downloaded successfully from network.\n"+
		"- Storage Remaining: %d GB\n", cmdResp.FolderName, cmdResp.AvailableStorage)
	fmt.Println(message)
}

// Deletes a file from the network
func deleteFile() {
	filePath := args[3]
	resp, err := client.SendRequest("deleteFile", protocol.DeleteFileRequest{FilePath: filePath})
	exitOnErr(err, "Error deleting "+filePath+": ")

	var cmdResp protocol.DeleteFileResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}
	message := fmt.Sprintf("\nFile '%v' deleted successfully from network.\n"+
		"- Storage Remaining: %v GB\n", cmdResp.FileName, cmdResp.AvailableStorage)
	fmt.Println(message)
}

// Deletes a folder from the network
func deleteFolder() {
	// since the deleting wont be local I cant really do much for it here
	folderPath := args[3]
	resp, err := client.SendRequest("deleteFolder", protocol.DeleteFolderRequest{FolderName: folderPath})
	exitOnErr(err, "Error deleting "+folderPath+": ")

	var cmdResp protocol.DeleteFolderResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}
	message := fmt.Sprintf("\nFolder '%v' deleted successfully from network.\n"+
		"- Storage Remaining: %v GB\n", cmdResp.FolderName, cmdResp.AvailableStorage)
	fmt.Println(message)
}

// Gets info about a file from the network
func fileInfo() {
	filePath := args[3]
	// idk what metadata we want exactly but this should be a rough estimate to serve as a model
	resp, err := client.SendRequest("fileInfo", protocol.FileInfoRequest{FilePath: filePath})
	exitOnErr(err, "Error getting file info for "+filePath+": ")

	var cmdResp protocol.FileInfoResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}
	message := fmt.Sprintf("\nFile '%v' info retrieved successfully from network.\n"+
		"- NodeID: %s@node-%v\n"+
		"- Date Added: %v\n"+
		"- Size: %v GB\n", cmdResp.FileName, cmdResp.Username, cmdResp.NodeID, cmdResp.DateAdded, cmdResp.Size)
	fmt.Println(message)
}

// Gets info about a folder from the network
func folderInfo() {
	folderName := args[3]
	// idk what metadata we want exactly but this should be a rough estimate to serve as a model
	resp, err := client.SendRequest("folderInfo", protocol.FolderInfoRequest{FolderName: folderName})
	exitOnErr(err, "Error getting folder info for "+folderName+": ")

	var cmdResp protocol.FolderInfoResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}
	message := fmt.Sprintf(
		"\nFolder Name: %s\n"+
			"	NodeID: %d\n"+
			"	Date Added: %v\n"+
			"	Size: %d GB\n"+
			"	Number of files: %d\n",
		cmdResp.FolderName, cmdResp.NodeID, cmdResp.DateAdded, cmdResp.Size, cmdResp.NumberOfFiles,
	)
	fmt.Println(message)
}

// Prints the current version of mos
func version() {
	resp, err := client.SendRequest("getVersion", protocol.VersionRequest{})
	exitOnErr(err, "Error getting version info: ")

	var cmdResp protocol.VersionResponse
	if err := mapToStruct(resp.Data, &cmdResp); err != nil {
		exitOnErr(err, "Error parsing response.")
	}
	message := fmt.Sprintf("\nmos version %v\n", cmdResp.Version)
	fmt.Println(message)
}

// help prints the help message
func help() {
	fmt.Println(helpMessage)
}

// ExitOnErr prints the message and error and exits if err is not nil
func exitOnErr(err error, msg string) {
	if err != nil {
		fmt.Println(msg, err)
		os.Exit(1)
	}
}

// mapToStruct maps data from an interface{} to a struct using JSON marshaling
func mapToStruct(data interface{}, v interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(jsonData, v)
}

// Shutdown stops the mosaic daemon and uninstalls the binaries
func Shutdown() {
	fmt.Println("Stopping mosaic daemon...")

	// Try to read PID file
	pidBytes, err := os.ReadFile("/tmp/mosaicd.pid")
	if err == nil {
		// Kill by PID
		var pid int
		_, err := fmt.Sscanf(string(pidBytes), "%d", &pid)
		exitOnErr(err, "Error killing by PID")
		process, err := os.FindProcess(pid)
		if err == nil {
			err := process.Signal(syscall.SIGTERM)
			exitOnErr(err, "Error sending SIGTERM to daemon process")
		}
		os.Remove("/tmp/mosaicd.pid")
	} else {
		// Fallback: kill by name
		err := exec.Command("pkill", "-f", "mosaicd").Run()
		exitOnErr(err, "Error killing daemon by name")
	}

	// Cleanup
	os.Remove("/tmp/mosaicd.sock")
	fmt.Println("✓ Daemon stopped")

	fmt.Println("")
	fmt.Println("Uninstalling...")

	// Uninstall binaries
	err = exec.Command("sudo", "rm", "-f", "/usr/local/bin/mos").Run()
	exitOnErr(err, "Error uninstalling mos binary")
	err = exec.Command("sudo", "rm", "-f", "/usr/local/bin/mosaicd").Run()
	exitOnErr(err, "Error uninstalling mosaicd binary")

	// Clean logs
	os.Remove("/tmp/mosaicd.log")

	fmt.Println("✓ Mosaic uninstalled")
}

// This is the old version of upload folder which I kept because I am proud of my recursive solution heh :)
// Unfortunately it will eventually have to go but not today!
/*
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
	storage := helpers.AvailableStorage()
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
			if isRoot || showSubFiles {
				fmt.Println("Uploading folder:", entry.Name())
			}
			//fmt.Println("Uploading folder:", entry.Name())
			err := UploadFolderRecursive(fullPath, showSubFiles, false)
			if err != nil {
				return err
			}
		} else {
			// replace with: uploadErr := uploadFile(fullPath)
			_, uploadErr := client.SendRequest("uploadFile", protocol.UploadRequest{
				Path: fullPath,
			})
			exitOnErr(uploadErr, "Error uploading file: "+fullPath)
			if isRoot || showSubFiles {
				fmt.Println("Uploading file:", entry.Name())
			}
		}
	}

	return nil
}
*/
