package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/melbahja/goph/v2"
	"github.com/spf13/cobra"
)

var (
	flagNode     string
	flagISO      string
	flagDelete   bool
	flagForce    bool
	flagCreateVM bool
	flagCores    int
	flagMemory   int
	flagDisk     int
	flagVmid     int
)

var rootCmd = &cobra.Command{
	Use:     "proxdk [host] [iso_name]",
	Version: "v0.1.0",
	Short:   "Manage ISOs in a Proxmox node's local ISO store over SSH",
	Long: `Interactive when run with no args. gh-style.
  Upload:  proxdk root@192.168.1.10 --node pve --iso ./debian-12.iso [my-name]
  Delete:  proxdk root@192.168.1.10 --node pve --iso debian-12.iso -D`,
	Args:         cobra.MaximumNArgs(2),
	SilenceUsage: true,
	RunE:         run,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().StringVar(&flagNode, "node", "", "Proxmox cluster node name (interactive if omitted)")
	rootCmd.Flags().StringVar(&flagISO, "iso", "", "local ISO path for upload, remote ISO name for delete (interactive if omitted)")
	rootCmd.Flags().BoolVarP(&flagDelete, "delete", "D", false, "delete the ISO from the node's store instead of uploading")
	rootCmd.Flags().BoolVarP(&flagForce, "force", "F", false, "overwrite an existing ISO without asking")
	rootCmd.Flags().BoolVar(&flagCreateVM, "create-vm", false, "after upload (or when the ISO already exists), create and boot a new VM from it")
	rootCmd.Flags().IntVar(&flagCores, "cores", 0, "vCPU count for the new VM (with --create-vm)")
	rootCmd.Flags().IntVar(&flagMemory, "memory", 0, "memory for the new VM in MiB (with --create-vm)")
	rootCmd.Flags().IntVar(&flagDisk, "disk", 0, "disk size for the new VM in GiB (with --create-vm)")
	rootCmd.Flags().IntVar(&flagVmid, "vmid", 0, "VM ID for the new VM (default: next free)")
}

func run(cmd *cobra.Command, args []string) error {
	if flagDelete && len(args) > 1 {
		return fmt.Errorf("iso_name argument is not used with --delete")
	}
	if flagDelete && flagForce {
		return fmt.Errorf("--force only applies to uploads")
	}
	if flagNode != "" {
		if err := validateName(flagNode); err != nil {
			return fmt.Errorf("invalid --node: %w", err)
		}
	}
	if flagCreateVM && flagDelete {
		return fmt.Errorf("--create-vm cannot be combined with --delete")
	}
	if !flagCreateVM && (flagCores != 0 || flagMemory != 0 || flagDisk != 0 || flagVmid != 0) {
		return fmt.Errorf("--cores/--memory/--disk/--vmid only apply with --create-vm")
	}
	if flagCreateVM {
		set := 0
		for _, v := range []int{flagCores, flagMemory, flagDisk} {
			if v != 0 {
				set++
			}
		}
		if set != 0 && set != 3 {
			return fmt.Errorf("--cores, --memory and --disk must be given together")
		}
		if set == 3 {
			if flagCores < 1 || flagCores > 8192 {
				return fmt.Errorf("invalid --cores %d (use 1-8192)", flagCores)
			}
			if flagMemory < 16 || flagMemory > 4194304 {
				return fmt.Errorf("invalid --memory %d (use 16-4194304 MiB)", flagMemory)
			}
			if flagDisk < 1 || flagDisk > 4194304 {
				return fmt.Errorf("invalid --disk %d (use 1-4194304 GiB)", flagDisk)
			}
		}
		if flagVmid != 0 {
			if err := validVMID(flagVmid); err != nil {
				return fmt.Errorf("invalid --vmid: %w", err)
			}
		}
	}

	deleteMode := flagDelete
	if len(args) == 0 && !flagDelete && !flagForce && flagISO == "" && flagNode == "" {
		// Fully interactive: pick the action first. "Create a VM from an
		// existing ISO" is its own flow; --create-vm is irrelevant here
		// (it only changes the upload flow, and this branch ignores
		// upload flags).
		idx, err := askChoice("What do you want to do?", []string{"Upload an ISO", "Delete an ISO", "Create a VM from an existing ISO"}, 0)
		if err != nil {
			return err
		}
		deleteMode = idx == 1
		if idx == 2 {
			host, err := askText("Proxmox host (user@addr): ")
			if err != nil {
				return err
			}
			user, addr, err := parseHost(host)
			if err != nil {
				return err
			}
			return runCreate(user, addr)
		}
	}

	if !deleteMode && flagISO != "" {
		// Validate the local ISO before prompting anything else or
		// attempting a connection.
		fi, err := os.Stat(flagISO)
		if err != nil {
			return fmt.Errorf("cannot read local ISO %q: %w", flagISO, err)
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("%q is not a regular file", flagISO)
		}
	}

	host := ""
	if len(args) > 0 {
		host = args[0]
	} else {
		var err error
		host, err = askText("Proxmox host (user@addr): ")
		if err != nil {
			return err
		}
	}
	user, addr, err := parseHost(host)
	if err != nil {
		return err
	}
	if deleteMode {
		return runDelete(user, addr)
	}
	return runUpload(user, addr, args)
}

// resolveNode returns the target node: the validated --node flag when given
// (checked to exist), otherwise chosen interactively from the live list.
func resolveNode(c *goph.Client, addr string) (string, error) {
	if flagNode != "" {
		ok, err := nodeExists(c, flagNode)
		if err != nil {
			return "", err
		}
		if !ok {
			avail, listErr := listNodes(c)
			if listErr != nil {
				return "", listErr
			}
			return "", fmt.Errorf("node %q not found on %s; available: %s", flagNode, addr, joinList(avail))
		}
		return flagNode, nil
	}
	nodes, err := listNodes(c)
	if err != nil {
		return "", err
	}
	if len(nodes) == 0 {
		return "", fmt.Errorf("no nodes found on %s", addr)
	}
	if len(nodes) == 1 {
		return nodes[0], nil
	}
	idx, err := askChoice("Select node", nodes, 0)
	if err != nil {
		return "", err
	}
	return nodes[idx], nil
}

func runUpload(user, addr string, args []string) error {
	isoPath := flagISO
	if isoPath == "" {
		var err error
		isoPath, err = askText("Path to local ISO file: ")
		if err != nil {
			return err
		}
	}
	fi, err := os.Stat(isoPath)
	if err != nil {
		return fmt.Errorf("cannot read local ISO %q: %w", isoPath, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", isoPath)
	}
	if !strings.HasSuffix(strings.ToLower(isoPath), ".iso") {
		fmt.Fprintf(os.Stderr, "Warning: %q does not end in .iso\n", isoPath)
	}

	name := ""
	if len(args) > 1 {
		name = args[1]
	} else {
		defaultName := filepath.Base(isoPath)
		name, err = askText(fmt.Sprintf("ISO name on host (default %s): ", defaultName))
		if err != nil {
			return err
		}
		if name == "" {
			name = defaultName
		}
	}
	name, err = targetName(name)
	if err != nil {
		return fmt.Errorf("invalid ISO name: %w", err)
	}

	c, err := connect(user, addr)
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
		return fmt.Errorf("%s not found on %s — is this a Proxmox host? (custom local storage paths are not supported in v0.1)", isoStoreDir, addr)
	}

	fs, err := newRemoteFS(c)
	if err != nil {
		return err
	}
	defer fs.Close()

	r, exists, err := fs.storeFileSize(name)
	if err != nil {
		return err
	}
	if exists {
		if flagForce {
			fmt.Printf("Overwriting existing ISO %s\n", name)
		} else {
			overwrite, err := askConfirm(fmt.Sprintf("ISO %q already exists on node %q (remote %d bytes, local %d bytes). Overwrite?", name, node, r, fi.Size()), false)
			if err != nil {
				return err
			}
			if !overwrite {
				fmt.Printf("Already present on %s — skipping.\n", node)
				return runProvision(c, node, name)
			}
		}
	}

	fmt.Printf("Uploading %s (%d bytes)…\n", name, fi.Size())
	if err := uploadISO(c, isoPath, name); err != nil {
		return err
	}

	got, exists, err := fs.storeFileSize(name)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("upload verify failed: %s missing after upload", name)
	}
	if got != fi.Size() {
		return fmt.Errorf("size mismatch after upload (%d != %d) — rerun to retry", got, fi.Size())
	}
	fmt.Printf("OK: %s (%d bytes) on node %s (%s)\n", name, fi.Size(), node, addr)
	return runProvision(c, node, name)
}

// runCreate is the interactive "Create a VM from an existing ISO" flow:
// pick an ISO already in the store, then provision a VM from it.
func runCreate(user, addr string) error {
	c, err := connect(user, addr)
	if err != nil {
		return err
	}
	defer c.Close()

	node, err := resolveNode(c, addr)
	if err != nil {
		return err
	}

	files, err := storeFiles(c)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no ISOs in the store on node %s", node)
	}
	idx, err := askChoice("Select ISO", files, 0)
	if err != nil {
		return err
	}
	return runProvision(c, node, files[idx])
}

func runDelete(user, addr string) error {
	name := flagISO
	if name == "" {
		var err error
		name, err = askText("ISO name to delete: ")
		if err != nil {
			return err
		}
	}
	name, err := targetName(name)
	if err != nil {
		return fmt.Errorf("invalid ISO name: %w", err)
	}

	c, err := connect(user, addr)
	if err != nil {
		return err
	}
	defer c.Close()

	node, err := resolveNode(c, addr)
	if err != nil {
		return err
	}

	fs, err := newRemoteFS(c)
	if err != nil {
		return err
	}
	defer fs.Close()

	_, exists, err := fs.storeFileSize(name)
	if err != nil {
		return err
	}
	if !exists {
		files, listErr := storeFiles(c)
		if listErr != nil {
			return listErr
		}
		return fmt.Errorf("ISO '%s' not found on node '%s'; store contains: %s", name, node, joinList(files))
	}

	ok, err := askConfirm(fmt.Sprintf("Delete %q from node %q? This cannot be undone.", name, node), false)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	if err := fs.removeStoreFile(name); err != nil {
		return err
	}
	_, exists, err = fs.storeFileSize(name)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("delete verify failed: %s still present", name)
	}
	fmt.Printf("Deleted %s from node %s (%s)\n", name, node, addr)
	return nil
}
