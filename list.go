package main

import (
	"fmt"
	"sort"
	"strings"
)

// runList prints the ISO store contents of a node: one line per file with
// size and modification time, then the backing storage's capacity. Files
// ending in ".tmp" are upload leftovers and flagged as such.
func runList(user, addr string) error {
	if err := applyProfile(user, addr); err != nil {
		return err
	}
	c, err := connect(user, addr, []string{profileFor(user, addr).Key})
	if err != nil {
		return err
	}
	defer c.Close()

	node, err := resolveNode(c, addr)
	if err != nil {
		return err
	}

	ok, err := storeExists(c)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s not found on %s — is this a Proxmox host? (the configured store may be wrong; check the storage setting of the host profile)", isoStoreDir, addr)
	}

	entries, err := listStoreEntries(c)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	for _, e := range entries {
		leftover := ""
		if strings.HasSuffix(e.Name, ".tmp") {
			leftover = "  (stale upload leftover)"
		}
		fmt.Printf("%-40s %10s  %s%s\n", e.Name, humanBytes(e.Size), e.ModTime.Format("2006-01-02 15:04"), leftover)
	}
	fmt.Printf("Total: %d file(s) in %s\n", len(entries), isoStoreDir)

	storage, path, totalB, usedB, err := isoStorageInfo(c, node)
	if err != nil {
		return err
	}
	free := totalB - usedB
	if free < 0 {
		free = 0
	}
	if path != "" {
		fmt.Printf("Storage %s (%s): %s used of %s, %s free\n", storage, path, humanBytes(usedB), humanBytes(totalB), humanBytes(free))
	} else {
		fmt.Printf("Storage %s: %s used of %s, %s free\n", storage, humanBytes(usedB), humanBytes(totalB), humanBytes(free))
	}
	remember(user, addr, node)
	return nil
}

// humanBytes renders a byte count as a short human-readable string.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
