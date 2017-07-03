package vhd

import (
	"fmt"
	"github.com/Microsoft/opengcs/service/libs/commonutils"
	"os"
)

// Converter converts a disk image to and from a VHD format.
type Converter interface {
	ConvertToVHD(f *os.File) error
	ConvertFromVHD(f *os.File) error
}

type fixedVHDConverter struct{}

func NewFixedVHDConverter() Converter {
	return fixedVHDConverter{}
}

// ConvertToVHD Implementation for converting disk image to a fixed VHD
func (fixedVHDConverter) ConvertToVHD(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		utils.LogMsgf("[ConvertToVHD] f.Stat failed with %s", err)
		return err
	}

	utils.LogMsgf("[ConvertToVHD] NewFixedVHDHeader with size = %d", uint64(info.Size()))

	hdr, err := NewFixedVHDHeader(uint64(info.Size()))
	if err != nil {
		utils.LogMsgf("[ConvertToVHD] NewFixedVHDHeader with %s", err)
		return err
	}

	hdrBytes, err := hdr.Bytes()
	if err != nil {
		utils.LogMsgf("[ConvertToVHD] hdr.Bytes with %s", err)
		return err
	}

	if _, err := f.WriteAt(hdrBytes, info.Size()); err != nil {
		utils.LogMsgf("[ConvertToVHD] f.WriteAt with %s", err)
		return err
	}
	return nil
}

// ConvertFromVHD converts a fixed VHD to a normal disk image.
func (fixedVHDConverter) ConvertFromVHD(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}

	if info.Size() < FixedVHDHeaderSize {
		return fmt.Errorf("Invalid input file: %s", f.Name())
	}

	if err := f.Truncate(info.Size() - FixedVHDHeaderSize); err != nil {
		return err
	}
	return nil
}
