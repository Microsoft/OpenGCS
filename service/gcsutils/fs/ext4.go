package fs

import (
	"github.com/Microsoft/opengcs/service/libs/commonutils"
	"os"
	"os/exec"
	"strconv"
)

// Ext4Fs implements the Filesystem interface for ext4.
//
// Ext4Fs makes the following assumptions about the ext4 file system.
//  - No journal or GDT table
//	- Extent tree (instead of direct/indirect block addressing)
//	- Hash tree directories (instead of linear directories)
//	- Inline symlinks if < 60 chars, but no inline directories or reg files
//  - sparse_super ext4 flags, so superblocks backups are in powers of 3, 5, 7
// 	- Directory entries take 1 block (even though its not true)
//  - All regular files/symlinks <= 128MB
type Ext4Fs struct {
	BlockSize uint64
	InodeSize uint64
	totalSize uint64
	numInodes uint64
}

// InitSizeContext creates the context for a new ext4 filesystem context
// Before calling set e.BlockSize and e.InodeSize to the desired values.
func (e *Ext4Fs) InitSizeContext() error {
	e.numInodes = 11                                                // ext4 has 11 reserved inodes
	e.totalSize = maxU64(2048+e.numInodes*e.InodeSize, e.BlockSize) // boot sector + super block is 2k
	return nil
}

func (e *Ext4Fs) CalcRegFileSize(fileName string, fileSize uint64) error {
	// 1 directory entry
	// 1 inode
	e.addInode()
	e.totalSize += e.BlockSize

	// Each extent can hold 32k blocks, so 32M of data, so 128MB can get held
	// in the 4 extends below the i_block.
	e.totalSize += alignN(fileSize, e.BlockSize)
	return nil
}

func (e *Ext4Fs) CalcDirSize(dirName string) error {
	// 1 directory entry for parent.
	// 1 inode with 2 directory entries ("." & ".." as data
	e.addInode()
	e.totalSize += 3 * e.BlockSize
	return nil
}

func (e *Ext4Fs) CalcSymlinkSize(srcName string, dstName string) error {
	e.addInode()
	if len(dstName) > 60 {
		// Not an inline symlink. The path is 1 extent max since MAX_PATH=4096
		e.totalSize += alignN(uint64(len(dstName)), e.BlockSize)
	}
	return nil
}

func (e *Ext4Fs) CalcHardlinkSize(srcName string, dstName string) error {
	// 1 directory entry (No additional inode)
	e.totalSize += e.BlockSize
	return nil
}

func (e *Ext4Fs) CalcCharDeviceSize(devName string, major uint64, minor uint64) error {
	e.addInode()
	return nil
}

func (e *Ext4Fs) CalcBlockDeviceSize(devName string, major uint64, minor uint64) error {
	e.addInode()
	return nil
}

func (e *Ext4Fs) CalcFIFOPipeSize(pipeName string) error {
	e.addInode()
	return nil
}

func (e *Ext4Fs) CalcSocketSize(sockName string) error {
	e.addInode()
	return nil
}

func (e *Ext4Fs) CalcAddExAttrSize(fileName string, attr string, data []byte, flags int) error {
	// Since xattr are stored in the inode, we don't use any more space
	return nil
}

func (e *Ext4Fs) FinalizeSizeContext() error {
	// Final adjustments to the size + inode
	// There are more metadata like Inode Table, block table.
	// For now, add 10% more to the size to take account for it.
	e.totalSize = uint64(float64(e.totalSize) * 1.10)
	e.numInodes = uint64(float64(e.numInodes) * 1.10)

	// Align to 64 * blocksize
	if e.totalSize%(64*e.BlockSize) != 0 {
		e.totalSize = alignN(e.totalSize, 64*e.BlockSize)
	}
	return nil
}

func (e *Ext4Fs) GetSizeInfo() FilesystemSizeInfo {
	return FilesystemSizeInfo{NumInodes: e.numInodes, TotalSize: e.totalSize}
}

func (e *Ext4Fs) CleanupSizeContext() error {
	// No resources need to be freed
	return nil
}

func (e *Ext4Fs) MakeFileSystem(file *os.File) error {
	blockSize := strconv.FormatUint(e.BlockSize, 10)
	inodeSize := strconv.FormatUint(e.InodeSize, 10)
	numInodes := strconv.FormatUint(e.numInodes, 10)
	utils.LogMsgf("making file system with: bs=%d is=%d numi=%d size=%d\n",
		e.BlockSize, e.InodeSize, e.numInodes, e.totalSize)

	err := exec.Command(
		"mkfs.ext4",
		"-O", "^has_journal,^resize_inode",
		file.Name(),
		"-N", numInodes,
		"-b", blockSize,
		"-I", inodeSize,
		"-F").Run()

	if err != nil {
		utils.LogMsgf("Running mkfs.ext4 failed with ... (%s)", err)
	}

	return err
}

func (e *Ext4Fs) MakeBasicFileSystem(file *os.File) error {
	return exec.Command("mkfs.ext4", file.Name(), "-F").Run()
}

func maxU64(x, y uint64) uint64 {
	if x > y {
		return x
	}
	return y
}

func alignN(n uint64, alignto uint64) uint64 {
	if n%alignto == 0 {
		return n
	}
	return n + alignto - n%alignto
}

func (e *Ext4Fs) addInode() {
	e.numInodes++
	e.totalSize += e.InodeSize
}
