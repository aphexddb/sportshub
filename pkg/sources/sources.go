package sources

type Camera struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListCameras returns video capture devices.
// On Windows this uses DirectShow via ffmpeg.
// On other platforms it will return stubs for now.
func ListCameras() ([]Camera, error) {
	return listCamerasImpl()
}
