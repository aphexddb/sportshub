package sources

// Camera is a video capture device, enriched with hardware identity so the UI can show a
// human-readable name (e.g. "Raspberry Pi Camera Module 3 (imx708) on CSI 0") and the rest of
// the app can pick capture settings appropriate to the hardware.
type Camera struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind,omitempty"`     // csi | usb | dshow | avfoundation | stub
	Model    string `json:"model,omitempty"`    // sensor (imx708) or product (C920)
	Vendor   string `json:"vendor,omitempty"`   // USB vendor, when resolved from usb.ids
	Location string `json:"location,omitempty"` // human location, e.g. "CSI 0", "USB"
}

// ListCameras returns video capture devices for the current OS.
func ListCameras() ([]Camera, error) { return listCamerasImpl() }
