//go:build linux

package devices

import "os"

// usbIDsPaths are the well-known locations of the open usb.ids database (from usbutils /
// hwdata), used to resolve USB vendor/product ids to human names.
var usbIDsPaths = []string{
	"/usr/share/misc/usb.ids",
	"/var/lib/usbutils/usb.ids",
	"/usr/share/hwdata/usb.ids",
}

// LookupUSB resolves vendor and product names for a USB id pair from the system usb.ids
// database. Returns empty strings if the database is unavailable or the id is unknown.
func LookupUSB(vid, pid string) (vendor, product string) {
	for _, p := range usbIDsPaths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		vendor, product = parseUSBIDs(f, vid, pid)
		f.Close()
		return vendor, product
	}
	return "", ""
}
