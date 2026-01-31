package protocol

type Request struct {
	Command string      `json:"command"`
	Data    interface{} `json:"data"`
}

type Response struct {
	Ok      bool        `json:"ok"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type UploadFileRequest struct {
	Path string `json:"path"`
}

type UploadFileResponse struct {
	Success          bool   `json:"success"`
	Details          string `json:"details"`
	FileName         string `json:"fileName"`
	AvailableStorage int    `json:"availableStorage"`
}

type UploadFolderRequest struct {
	FolderPath string `json:"folderPath"`
}

type UploadFolderResponse struct {
	Success          bool   `json:"success"`
	Details          string `json:"details"`
	FolderName       string `json:"folderName"`
	AvailableStorage int    `json:"availableStorage"`
}

type NetworkStatusRequest struct {
	Path string `json:"path"`
}

type NetworkStatusResponse struct {
	Success          bool   `json:"success"`
	Details          string `json:"details"`
	NetworkStorage   int    `json:"networkStorage"`
	AvailableStorage int    `json:"availableStorage"`
	StorageUsed      int    `json:"storageUsed"`
	Peers            int    `json:"peers"`
}

type JoinRequest struct {
	ServerAddress string `json:"ServerAddress"`
}

type JoinResponse struct {
	Success bool   `json:"success"`
	Details string `json:"details"`
}

type NodeStatusRequest struct {
	ID string `json:"id"`
}

type NodeStatusResponse struct {
	Success      bool   `json:"success"`
	Details      string `json:"details"`
	Username     string `json:"username"`
	ID           string `json:"id"`
	StorageShare int    `json:"storageShare"`
}

type LoginKeyRequest struct {
	Key string `json:"key"`
}

type LoginKeyResponse struct {
	Success     bool   `json:"success"`
	Details     string `json:"details"`
	CurrentNode int    `json:"currentNode"`
	Username    string `json:"username"`
}

type SetStorageRequest struct {
	Amount int `json:"amount"`
	Node   int `json:"node"`
}

type SetStorageResponse struct {
	Success          bool   `json:"success"`
	Details          string `json:"details"`
	CurrentNode      int    `json:"currentNode"`
	NodeStorage      int    `json:"nodeStorage"`
	AvailableStorage int    `json:"availableStorage"`
	Username         string `json:"username"`
}

type EmptyStorageRequest struct {
	AccountID int `json:"accountID"`
}

type EmptyStorageResponse struct {
	Success          bool   `json:"success"`
	Details          string `json:"details"`
	StorageDeleted   int    `json:"storageDeleted"`
	AvailableStorage int    `json:"availableStorage"`
	Username         string `json:"username"`
}

type StatusAccountRequest struct {
	AccountID int `json:"accountID"`
}

type StatusAccountResponse struct {
	Success          bool     `json:"success"`
	Details          string   `json:"details"`
	Nodes            []string `json:"nodes"`
	GivenStorage     int      `json:"givenStorage"`
	AvailableStorage int      `json:"availableStorage"`
	UsedStorage      int      `json:"usedStorage"`
	Username         string   `json:"username"`
}

type LogoutRequest struct {
	AccountID int `json:"accountID"`
}

type LogoutResponse struct {
	Success  bool   `json:"success"`
	Details  string `json:"details"`
	Username string `json:"username"`
}

type GetPeersRequest struct {
}

type GetPeersResponse struct {
	Success bool   `json:"success"`
	Details string `json:"details"`
	Peers   []Peer `json:"peers"`
}

type Peer struct {
	Username      string `json:"username"`
	NodeID        int    `json:"nodeID"`
	StorageShared int    `json:"storageShared"`
}

type LeaveNetworkRequest struct {
	AccountID int `json:"accountID"`
}

type LeaveNetworkResponse struct {
	Success  bool   `json:"success"`
	Details  string `json:"details"`
	Username string `json:"username"`
}

type ListFilesRequest struct {
	AccountID int `json:"accountID"`
}

type ListFilesResponse struct {
	Success  bool     `json:"success"`
	Details  string   `json:"details"`
	Username string   `json:"username"`
	Files    []string `json:"files"`
}

type DeleteFileRequest struct {
	FilePath string `json:"filePath"`
}

type DeleteFileResponse struct {
	Success          bool   `json:"success"`
	Details          string `json:"details"`
	FileName         string `json:"name"`
	AvailableStorage int    `json:"availableStorage"`
}

type DeleteFolderRequest struct {
	FolderName string `json:"folderPath"`
}

type DeleteFolderResponse struct {
	Success          bool   `json:"success"`
	Details          string `json:"details"`
	FolderName       string `json:"name"`
	AvailableStorage int    `json:"availableStorage"`
}
type DownloadFileRequest struct {
	FilePath string `json:"filePath"`
}

type DownloadFileResponse struct {
	Success          bool   `json:"success"`
	Details          string `json:"details"`
	FileName         string `json:"name"`
	AvailableStorage int    `json:"availableStorage"`
}

type DownloadFolderRequest struct {
	FolderPath string `json:"folderPath"`
}

type DownloadFolderResponse struct {
	Success          bool   `json:"success"`
	Details          string `json:"details"`
	FolderName       string `json:"name"`
	AvailableStorage int    `json:"availableStorage"`
}

type FileInfoRequest struct {
	FilePath string `json:"filePath"`
}

type FileInfoResponse struct {
	Success   bool   `json:"success"`
	Details   string `json:"details"`
	FileName  string `json:"name"`
	Username  string `json:"username"`
	NodeID    int    `json:"nodeID"`
	DateAdded string `json:"dateAdded"`
	Size      int    `json:"size"`
}

type FolderInfoRequest struct {
	FolderName string `json:"folderName"`
}

type FolderInfoResponse struct {
	Success       bool   `json:"success"`
	Details       string `json:"details"`
	FolderName    string `json:"name"`
	NodeID        int    `json:"nodeID"`
	DateAdded     string `json:"dateAdded"`
	Size          int    `json:"size"`
	NumberOfFiles int    `json:"numberOfFiles"`
}

type VersionRequest struct {
}
type VersionResponse struct {
	Success bool   `json:"success"`
	Details string `json:"details"`
	Version string `json:"version"`
}
