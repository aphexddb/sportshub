//go:build !windows && !linux && !darwin

package sources

func listCamerasImpl() ([]Camera, error) {
	return []Camera{{ID: "video=stub", Name: "Stub Camera (unsupported OS)"}}, nil
}
