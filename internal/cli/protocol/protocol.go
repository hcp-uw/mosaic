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

type UploadRequest struct {
	Path string `json:"path"`
}

type UploadResponse struct {
	Success          bool   `json:"success"`
	Details          string `json:"details"`
	Name             string `json:"name"`
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
}

type JoinResponse struct {
	Success bool   `json:"success"`
	Details string `json:"details"`
	Peers   int    `json:"peers"`
}

// username, id, storShared, storUsed, err := nodeStatus()

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
