//go:build !dot

package screen

import "os"

// handOffPath, when present, gives the panel and the touchscreen to another program (an external UI
// drawing to the framebuffer itself): the display and touch components are not registered, and
// everything else runs as usual. Removing the file and restarting gives them back.
const handOffPath = "/data/techo5-linux/external-ui"

// HandedOff reports whether another program owns the panel and the touchscreen.
func HandedOff() bool {
	_, err := os.Stat(handOffPath)
	return err == nil
}
