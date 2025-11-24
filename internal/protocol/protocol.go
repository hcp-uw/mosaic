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
	Success bool   `json:"success"`
	Details string `json:"details"`
}

type JoinRequest struct {
	SessionID string `json:"sessionID"`
}
