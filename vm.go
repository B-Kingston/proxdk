package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/melbahja/goph/v2"
)

// clusterResource is one entry of "pvesh get /cluster/resources".
// maxcpu is a JSON number in PVE's schema; the memory and disk fields are
// byte counters.
type clusterResource struct {
	Type    string  `json:"type"`
	Node    string  `json:"node"`
	VMID    int     `json:"vmid"`
	Name    string  `json:"name"`
	MaxCPU  float64 `json:"maxcpu"`
	MaxMem  int64   `json:"maxmem"`
	Mem     int64   `json:"mem"`
	MaxDisk int64   `json:"maxdisk"`
	Disk    int64   `json:"disk"`
}

// parseNodeTotals extracts a node's CPU capacity and memory capacity/usage
// from "pvesh get /cluster/resources" output. The node entry is RRD-fed by
// pvestatd (~10 s stale), which is fine for an allocation UI. A missing or
// malformed node entry is an error, never a silent zero.
func parseNodeTotals(data []byte, node string) (coresTotal, memoryTotalMiB, memoryUsedMiB int64, err error) {
	var entries []clusterResource
	if err := json.Unmarshal(data, &entries); err != nil {
		return 0, 0, 0, fmt.Errorf("cannot parse cluster resources: %w", err)
	}
	for _, e := range entries {
		if e.Type == "node" && e.Node == node {
			if e.MaxCPU <= 0 || e.MaxMem <= 0 || e.Mem < 0 {
				return 0, 0, 0, fmt.Errorf("node %q entry is missing maxcpu/maxmem/mem", node)
			}
			return int64(e.MaxCPU), e.MaxMem >> 20, e.Mem >> 20, nil
		}
	}
	return 0, 0, 0, fmt.Errorf("node %q not found in cluster resources", node)
}

// parseVMAllocation sums the reserved resources of the QEMU VMs on node
// from "pvesh get /cluster/resources" output (the "free" a new VM can
// take). LXC containers are deliberately excluded: the picker sizes a qm
// VM. Zero VMs yield zeros.
func parseVMAllocation(data []byte, node string) (coresUsed, memoryUsedMiB, diskUsedGiB int64, err error) {
	var entries []clusterResource
	if err := json.Unmarshal(data, &entries); err != nil {
		return 0, 0, 0, fmt.Errorf("cannot parse cluster resources: %w", err)
	}
	for _, e := range entries {
		if e.Type != "qemu" || e.Node != node {
			continue
		}
		if e.MaxCPU <= 0 || e.MaxMem <= 0 {
			return 0, 0, 0, fmt.Errorf("qemu entry %d is missing maxcpu/maxmem", e.VMID)
		}
		coresUsed += int64(e.MaxCPU)
		memoryUsedMiB += e.MaxMem >> 20
		diskUsedGiB += e.MaxDisk >> 30
	}
	return coresUsed, memoryUsedMiB, diskUsedGiB, nil
}

// buildSnapshot loads the node's capacity and current VM reservation for
// the resource picker. Disk totals are filled in separately by
// pickVMStorage: VM disks live on a storage, not on the node.
func buildSnapshot(c *goph.Client, node string) (resourceSnapshot, error) {
	out, err := runRemote(c, "pvesh", "get", "/cluster/resources", "--output-format", "json")
	if err != nil {
		return resourceSnapshot{}, fmt.Errorf("cannot load cluster resources: %w", err)
	}
	coresTotal, memoryTotalMiB, _, err := parseNodeTotals(out, node)
	if err != nil {
		return resourceSnapshot{}, err
	}
	coresUsed, memoryUsedMiB, _, err := parseVMAllocation(out, node)
	if err != nil {
		return resourceSnapshot{}, err
	}
	return resourceSnapshot{
		coresTotal:     coresTotal,
		coresUsed:      coresUsed,
		memoryTotalMiB: memoryTotalMiB,
		memoryUsedMiB:  memoryUsedMiB,
	}, nil
}

// parseNextID parses "pvesh get /cluster/nextid --output-format json"
// output. pvesh returns the ID as a JSON string ("103"); accept a bare
// JSON number too, in case a future pvesh version changes the type.
func parseNextID(data []byte) (int, error) {
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return 0, fmt.Errorf("cannot parse cluster nextid: %w", err)
	}
	var vmid int
	if err := json.Unmarshal(raw, &vmid); err == nil {
		return vmid, validVMID(vmid)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("cannot parse cluster nextid %q", s)
		}
		return n, validVMID(n)
	}
	return 0, fmt.Errorf("cannot parse cluster nextid: unexpected type")
}

// nextVMID returns the next free VMID on the cluster.
func nextVMID(c *goph.Client) (int, error) {
	out, err := runRemote(c, "pvesh", "get", "/cluster/nextid", "--output-format", "json")
	if err != nil {
		return 0, fmt.Errorf("cannot load next VMID: %w", err)
	}
	return parseNextID(out)
}

// storageEntry is one entry of "pvesh get /nodes/<node>/storage".
type storageEntry struct {
	Storage string `json:"storage"`
	Content string `json:"content"`
	Enabled int    `json:"enabled"`
	Active  int    `json:"active"`
	Path    string `json:"path"`
	Total   int64  `json:"total"`
	Used    int64  `json:"used"`
}

// parseStorageList picks the first enabled, active storage whose content
// includes "images" from "pvesh get /nodes/<node>/storage" output and
// returns its capacity and usage in GiB. On default installs this is
// local-lvm or local-zfs.
func parseStorageList(data []byte) (storage string, totalGiB, usedGiB int64, err error) {
	var entries []storageEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return "", 0, 0, fmt.Errorf("cannot parse storage list: %w", err)
	}
	var seen []string
	for _, e := range entries {
		if e.Storage != "" {
			seen = append(seen, e.Storage)
		}
		if e.Enabled != 1 || e.Active != 1 || e.Total <= 0 {
			continue
		}
		for _, content := range strings.Split(e.Content, ",") {
			if content == "images" {
				return e.Storage, e.Total >> 30, e.Used >> 30, nil
			}
		}
	}
	if len(seen) == 0 {
		return "", 0, 0, fmt.Errorf("no storage with VM images content is active on this node")
	}
	return "", 0, 0, fmt.Errorf("no storage with VM images content is active on this node (seen: %s)", joinList(seen))
}

// pickVMStorage returns the first storage on node able to hold VM disks
// (enabled, active, content includes "images") with its capacity and usage
// in GiB.
func pickVMStorage(c *goph.Client, node string) (storage string, totalGiB, usedGiB int64, err error) {
	out, err := runRemote(c, "pvesh", "get", "/nodes/"+node+"/storage", "--output-format", "json")
	if err != nil {
		return "", 0, 0, fmt.Errorf("cannot load storage list: %w", err)
	}
	return parseStorageList(out)
}

// parseIsoStorage finds the storage holding the ISO store from "pvesh get
// /nodes/<node>/storage" output and returns its ID, path, and capacity
// usage in bytes. PVE dir storages keep ISOs in <path>/template/iso, so
// the store dir is matched against <path>/template/iso; when nothing
// matches, the first enabled, active storage with iso content is used.
func parseIsoStorage(data []byte, storeDir string) (storage, path string, totalB, usedB int64, err error) {
	var entries []storageEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return "", "", 0, 0, fmt.Errorf("cannot parse storage list: %w", err)
	}
	var seen []string
	var fallback *storageEntry
	for i := range entries {
		e := &entries[i]
		if e.Storage != "" {
			seen = append(seen, e.Storage)
		}
		if e.Enabled != 1 || e.Active != 1 || e.Total <= 0 {
			continue
		}
		if !hasContent(e.Content, "iso") {
			continue
		}
		if e.Path != "" && strings.TrimSuffix(e.Path, "/")+"/template/iso" == storeDir {
			return e.Storage, e.Path, e.Total, e.Used, nil
		}
		if fallback == nil {
			fallback = e
		}
	}
	if fallback != nil {
		return fallback.Storage, fallback.Path, fallback.Total, fallback.Used, nil
	}
	if len(seen) == 0 {
		return "", "", 0, 0, fmt.Errorf("no storage with iso content is active on this node")
	}
	return "", "", 0, 0, fmt.Errorf("no storage with iso content is active on this node (seen: %s)", joinList(seen))
}

// hasContent reports whether a storage content list includes want.
func hasContent(content, want string) bool {
	for _, c := range strings.Split(content, ",") {
		if c == want {
			return true
		}
	}
	return false
}

// vmNameFromISO derives a Proxmox VM name from an ISO name: the ISO name
// without its .iso/.ISO suffix, valid per validVMName. ok=false when the
// result would violate PVE's name rules (the VM is then created without a
// name).
func vmNameFromISO(isoName string) (name string, ok bool) {
	name = strings.TrimSuffix(isoName, ".iso")
	if name == isoName {
		name = strings.TrimSuffix(isoName, ".ISO")
	}
	if validVMName(name) != nil {
		return "", false
	}
	return name, true
}

// qmCreateArgv builds the argv of the allowlisted "qm create" command for
// a new VM: an optional --name pair, then the fixed options in the exact
// order allowQMCreate expects. Every value is validated locally, so the
// command is guaranteed to pass the remote gate. diskStorage holds the VM
// disk, isoStorage holds the boot ISO (the storage the ISO store lives
// on); both are validated storage IDs.
func qmCreateArgv(vmid int, sel resourceSelection, diskStorage, isoStorage, isoName string) ([]string, error) {
	if err := validVMID(vmid); err != nil {
		return nil, err
	}
	if sel.cores < 1 || sel.cores > 8192 {
		return nil, fmt.Errorf("invalid core count %d (use 1-8192)", sel.cores)
	}
	if sel.memoryMiB < 16 || sel.memoryMiB > 4194304 {
		return nil, fmt.Errorf("invalid memory %d MiB (use 16-4194304)", sel.memoryMiB)
	}
	if sel.diskGiB < 1 || sel.diskGiB > 4194304 {
		return nil, fmt.Errorf("invalid disk size %dG (use 1-4194304)", sel.diskGiB)
	}
	if err := validStorageID(diskStorage); err != nil {
		return nil, err
	}
	if err := validStorageID(isoStorage); err != nil {
		return nil, err
	}
	if err := validateName(isoName); err != nil {
		return nil, err
	}
	argv := []string{"qm", "create", strconv.Itoa(vmid)}
	if name, ok := vmNameFromISO(isoName); ok {
		argv = append(argv, "--name", name)
	}
	argv = append(argv,
		"--sockets", "1",
		"--cores", strconv.FormatInt(sel.cores, 10),
		"--memory", strconv.FormatInt(sel.memoryMiB, 10),
		"--scsi0", diskStorage+":"+strconv.FormatInt(sel.diskGiB, 10),
		"--ide2", isoStorage+":iso/"+isoName+",media=cdrom",
		"--boot", "order=ide2",
		"--net0", "virtio,bridge=vmbr0",
		"--scsihw", "virtio-scsi-pci",
	)
	return argv, nil
}

// parseVMStatus parses "qm status <vmid>" output ("status: stopped").
func parseVMStatus(out []byte) (string, error) {
	line := strings.TrimSpace(string(out))
	if !strings.HasPrefix(line, "status: ") {
		return "", fmt.Errorf("unexpected qm status output %q", line)
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "status: ")), nil
}

// validateSelection rejects an allocation that cannot produce a usable
// VM. The picker can yield 0 for a fully allocated resource, and a VM
// without CPU, RAM, or disk is not worth creating.
func validateSelection(sel resourceSelection) error {
	if sel.cores < 1 {
		return fmt.Errorf("no free CPU cores on the node — nothing to allocate")
	}
	if sel.memoryMiB < 16 {
		return fmt.Errorf("no free memory on the node — nothing to allocate")
	}
	if sel.diskGiB < 1 {
		return fmt.Errorf("no free disk space on the target storage — nothing to allocate")
	}
	return nil
}

// createVM runs the allowlisted qm create argv and verifies the VM exists
// in the stopped state.
func createVM(c *goph.Client, argv []string, vmid int) error {
	if _, err := runRemote(c, argv...); err != nil {
		return err
	}
	out, err := runRemote(c, "qm", "status", strconv.Itoa(vmid))
	if err != nil {
		return err
	}
	status, err := parseVMStatus(out)
	if err != nil {
		return err
	}
	if status != "stopped" {
		return fmt.Errorf("VM %d created but verify failed: status %q", vmid, status)
	}
	return nil
}

// startVM boots the VM and verifies it reaches the running state.
func startVM(c *goph.Client, vmid int) error {
	if _, err := runRemote(c, "qm", "start", strconv.Itoa(vmid)); err != nil {
		return err
	}
	out, err := runRemote(c, "qm", "status", strconv.Itoa(vmid))
	if err != nil {
		return err
	}
	status, err := parseVMStatus(out)
	if err != nil {
		return err
	}
	if status != "running" {
		return fmt.Errorf("VM %d started but verify failed: status %q", vmid, status)
	}
	return nil
}

// runProvision creates and boots a new VM from an ISO that is present in
// the node's store, sized by the resource selection (live picker in
// interactive mode, --cores/--memory/--disk flags otherwise), and prints
// progress. Declining a prompt exits 0, like "already present — skipping":
// the returned vmid is 0 then, and the created VM's ID otherwise.
func runProvision(c *goph.Client, node, isoName string) (int, error) {
	interactive := flagCores == 0 // run() enforces all-or-none of the resource flags
	if interactive {
		ok, err := askConfirm("Create and boot a new VM from this ISO?", true)
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, nil
		}
	}

	var (
		sel               resourceSelection
		storage           string
		totalGiB, usedGiB int64
		err               error
	)
	if interactive {
		snap, err := buildSnapshot(c, node)
		if err != nil {
			return 0, err
		}
		storage, totalGiB, usedGiB, err = pickVMStorage(c, node)
		if err != nil {
			return 0, err
		}
		snap.diskTotalGiB, snap.diskUsedGiB = totalGiB, usedGiB
		sel, err = runResourcePicker(snap)
		if err != nil {
			return 0, err
		}
		if err := validateSelection(sel); err != nil {
			return 0, err
		}
	} else {
		sel = resourceSelection{cores: int64(flagCores), memoryMiB: int64(flagMemory), diskGiB: int64(flagDisk)}
	}

	vmid := flagVmid
	if vmid == 0 {
		vmid, err = nextVMID(c)
		if err != nil {
			return 0, err
		}
	}
	if interactive {
		answer, err := askText(fmt.Sprintf("VM ID (default %d): ", vmid))
		if err != nil {
			return 0, err
		}
		if answer != "" {
			n, perr := strconv.Atoi(answer)
			if perr != nil || validVMID(n) != nil {
				return 0, fmt.Errorf("invalid VM ID %q (use 100-999999999)", answer)
			}
			vmid = n
		}
	}

	if !interactive {
		storage, totalGiB, usedGiB, err = pickVMStorage(c, node)
		if err != nil {
			return 0, err
		}
	}
	if sel.diskGiB > totalGiB-usedGiB {
		return 0, fmt.Errorf("disk %dG exceeds free space on %s (%dG available)", sel.diskGiB, storage, totalGiB-usedGiB)
	}

	// The ISO store may live on a non-default storage; resolve its ID so
	// the cdrom volume references the right one.
	isoStorage, _, _, _, err := isoStorageInfo(c, node)
	if err != nil {
		return 0, err
	}

	argv, err := qmCreateArgv(vmid, sel, storage, isoStorage, isoName)
	if err != nil {
		return 0, err
	}
	name, _ := vmNameFromISO(isoName)
	if name == "" {
		name = "unlabeled"
	}
	fmt.Printf("Creating VM %d (%s): %d vcores · %d MiB RAM · %dG disk on %s, booting %s:iso/%s\n",
		vmid, name, sel.cores, sel.memoryMiB, sel.diskGiB, storage, isoStorage, isoName)
	if interactive {
		ok, err := askConfirm("Proceed?", true)
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, nil
		}
	}

	if err := createVM(c, argv, vmid); err != nil {
		return 0, err
	}
	if err := startVM(c, vmid); err != nil {
		return 0, fmt.Errorf("VM %d created but failed to start: %w", vmid, err)
	}
	fmt.Printf("VM %d (%s) is running on node %s\n", vmid, name, node)
	return vmid, nil
}

// parseVMList extracts the QEMU VM entries of "pvesh get
// /cluster/resources" output.
func parseVMList(data []byte) ([]clusterResource, error) {
	var entries []clusterResource
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("cannot parse cluster resources: %w", err)
	}
	var vms []clusterResource
	for _, e := range entries {
		if e.Type == "qemu" {
			vms = append(vms, e)
		}
	}
	return vms, nil
}

// vmInfo returns the name of the VM with the given ID on node, or an
// error when the VM is missing or lives on another node. The empty name is
// a valid result (VMs can be unnamed).
func vmInfo(c *goph.Client, node string, vmid int) (string, error) {
	out, err := runRemote(c, "pvesh", "get", "/cluster/resources", "--output-format", "json")
	if err != nil {
		return "", fmt.Errorf("cannot load cluster resources: %w", err)
	}
	vms, err := parseVMList(out)
	if err != nil {
		return "", err
	}
	for _, e := range vms {
		if e.VMID == vmid {
			if e.Node != node {
				return "", fmt.Errorf("VM %d is on node %s, not %s", vmid, e.Node, node)
			}
			return e.Name, nil
		}
	}
	return "", fmt.Errorf("VM %d not found on node %s", vmid, node)
}

// vmGone reports whether qm status no longer knows the VM — the
// verification that a destroy took effect. qm reports a destroyed VM as
// "not found" on stderr; the combined output lands in the error string.
func vmGone(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "does not exist")
}

// runDeleteVM destroys a VM after confirmation and verifies it is gone.
// Declining exits 0. Destruction is confined to VMs proxdk created (the
// VM ledger); anything else is refused before connecting.
func runDeleteVM(user, addr string, vmid int) error {
	if err := applyProfile(user, addr); err != nil {
		return err
	}
	if !isDeletableVM(user, addr, vmid) {
		return fmt.Errorf("refusing to delete VM %d: it is not tracked as a VM proxdk created on %s. Destroy it through the Proxmox UI, or edit the vms list in the config file", vmid, hostKey(user, addr))
	}
	key := profileFor(user, addr).Key
	c, err := connect(user, addr, []string{key})
	if err != nil {
		return err
	}
	defer c.Close()

	node, err := resolveNode(c, addr)
	if err != nil {
		return err
	}

	name, err := vmInfo(c, node, vmid)
	if err != nil {
		return err
	}
	display := name
	if display == "" {
		display = "unlabeled"
	}

	ok, err := askConfirm(fmt.Sprintf("Delete VM %d (%s) from node %q? This cannot be undone.", vmid, display, node), false)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	if _, err := runRemote(c, "qm", "destroy", strconv.Itoa(vmid)); err != nil {
		return err
	}
	if _, err := runRemote(c, "qm", "status", strconv.Itoa(vmid)); err == nil {
		return fmt.Errorf("delete verify failed: VM %d still exists", vmid)
	} else if !vmGone(err) {
		return fmt.Errorf("delete verify failed: %w", err)
	}
	fmt.Printf("Deleted VM %d (%s) from node %s (%s)\n", vmid, display, node, addr)
	vmLedgerRemove(user, addr, vmid)
	remember(user, addr, node)
	return nil
}
