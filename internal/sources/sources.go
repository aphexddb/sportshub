package sources

type Camera struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListCameras returns video capture devices for the current OS.
func ListCameras() ([]Camera, error) { return listCamerasImpl() }
