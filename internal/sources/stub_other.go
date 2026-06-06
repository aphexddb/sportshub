//go:build !windows && !linux && !darwin

package sources

import "sportshub/internal/devices"

func listCamerasImpl() ([]Camera, error) {
	return []Camera{{ID: "video=stub", Name: "Stub Camera (unsupported OS)", Kind: string(devices.KindStub)}}, nil
}
