//go:build !windows

package sources

func listCamerasImpl() ([]Camera, error) {
	return []Camera{
		{ID: "video=stub", Name: "Stub Camera (Linux/Pi only)"},
	}, nil
}
