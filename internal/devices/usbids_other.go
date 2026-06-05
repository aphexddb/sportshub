//go:build !linux

package devices

// LookupUSB is a no-op off Linux: Windows DirectShow and macOS avfoundation already report
// human-readable device names, and the usb.ids database isn't generally present there.
func LookupUSB(vid, pid string) (vendor, product string) { return "", "" }
