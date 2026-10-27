// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package driverutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/docker/go-units"
	"github.com/lima-vm/go-qcow2reader/image"
	"github.com/lima-vm/go-qcow2reader/image/vhdx"
	"github.com/sirupsen/logrus"

	contfs "github.com/containerd/continuity/fs"
	"github.com/lima-vm/lima/v2/pkg/imgutil/nativeimgutil"
	"github.com/lima-vm/lima/v2/pkg/imgutil/proxyimgutil"
	"github.com/lima-vm/lima/v2/pkg/iso9660util"
	"github.com/lima-vm/lima/v2/pkg/limatype/filenames"
	"github.com/lima-vm/lima/v2/pkg/osutil"
)

// MigrateDiskLayout creates symlinks from the current filenames (disk, iso) to
// the legacy filenames (diffdisk, basedisk) used by older Lima versions.
// The original files are left in place so older Lima versions can still use them.
func MigrateDiskLayout(instDir string) error {
	diskPath := filepath.Join(instDir, filenames.Disk)
	if osutil.FileExists(diskPath) {
		return nil // already migrated or new instance
	}

	diffDiskPath := filepath.Join(instDir, filenames.DiffDiskLegacy)
	if osutil.FileExists(diffDiskPath) {
		logrus.Infof("Creating symlink %#q -> %#q", filenames.Disk, filenames.DiffDiskLegacy)
		if err := os.Symlink(filenames.DiffDiskLegacy, diskPath); err != nil {
			return fmt.Errorf("failed to symlink %#q to %#q: %w", filenames.Disk, filenames.DiffDiskLegacy, err)
		}
	}

	baseDiskPath := filepath.Join(instDir, filenames.BaseDiskLegacy)
	isoPath := filepath.Join(instDir, filenames.ISO)
	if osutil.FileExists(baseDiskPath) && !osutil.FileExists(isoPath) {
		isISO, err := iso9660util.IsISO9660(baseDiskPath)
		if err != nil {
			return err
		}
		if isISO {
			logrus.Infof("Creating symlink %#q -> %#q", filenames.ISO, filenames.BaseDiskLegacy)
			if err := os.Symlink(filenames.BaseDiskLegacy, isoPath); err != nil {
				return fmt.Errorf("failed to symlink %#q to %#q: %w", filenames.ISO, filenames.BaseDiskLegacy, err)
			}
		}
		// Non-ISO basedisk is a legacy qcow2 backing file; leave it for QEMU to resolve.
	}

	return nil
}

// EnsureDiskSymlink makes the VM disk live in diskDir instead of the instance
// directory, by creating the symlink <instDir>/disk -> <diskDir>/disk.
// The symlink is created before the disk file exists; the disk file is created
// later through the symlink (see osutil.ResolveSymlink).
// It is a NOP when diskDir is empty.
func EnsureDiskSymlink(instDir, diskDir string) error {
	if diskDir == "" {
		return nil
	}
	if fi, err := os.Stat(diskDir); err == nil && !fi.IsDir() {
		return fmt.Errorf("disk path %#q is not a directory", diskDir)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	instDiskPath := filepath.Join(instDir, filenames.Disk)
	realDiskPath, err := filepath.Abs(filepath.Join(diskDir, filenames.Disk))
	if err != nil {
		return err
	}
	absInstDir, err := filepath.Abs(instDir)
	if err != nil {
		return err
	}
	// A disk inside the instance directory would make the symlink point to
	// itself, and the disk would be removed together with the instance.
	if rel, err := filepath.Rel(absInstDir, realDiskPath); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("disk path %#q must be outside of the instance directory %#q", diskDir, instDir)
	}

	if osutil.IsSymlink(instDiskPath) {
		target, err := os.Readlink(instDiskPath)
		if err != nil {
			return fmt.Errorf("failed to read symlink %#q: %w", instDiskPath, err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(instDir, target)
		}
		if filepath.Clean(target) != filepath.Clean(realDiskPath) {
			return fmt.Errorf("%#q already points to %#q instead of %#q", instDiskPath, target, realDiskPath)
		}
		return nil
	}
	if _, err := os.Lstat(instDiskPath); err == nil {
		return fmt.Errorf("%#q already exists and is not a symlink", instDiskPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Refuse to reuse an existing disk file, otherwise two instances would
	// share the same disk.
	if _, err := os.Lstat(realDiskPath); err == nil {
		return fmt.Errorf("disk file %#q already exists", realDiskPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(realDiskPath), 0o700); err != nil {
		return err
	}
	logrus.Infof("Creating symlink %#q -> %#q", instDiskPath, realDiskPath)
	if err := os.Symlink(realDiskPath, instDiskPath); err != nil {
		return fmt.Errorf("failed to symlink %#q to %#q: %w", instDiskPath, realDiskPath, err)
	}
	return nil
}

// EnsureDisk creates the VM disk from the downloaded image.
// For ISO images, it renames the image to "iso" and creates an empty disk.
// For non-ISO images, it converts the image to "disk" and removes the original.
func EnsureDisk(ctx context.Context, instDir, diskSize string, diskImageFormat image.Type) error {
	diskPath := filepath.Join(instDir, filenames.Disk)
	// The disk may be a symlink to a file outside of the instance directory
	// (see EnsureDiskSymlink); then the target file has to be created, renamed,
	// and removed instead of the symlink itself.
	realDiskPath := osutil.ResolveSymlink(diskPath)
	if _, err := os.Stat(diskPath); err == nil || !errors.Is(err, os.ErrNotExist) {
		return err
	}

	imagePath := filepath.Join(instDir, filenames.Image)
	isISO, err := iso9660util.IsISO9660(imagePath)
	if err != nil {
		return err
	}

	diskSizeInBytes, _ := units.RAMInBytes(diskSize)
	diskUtil := proxyimgutil.NewDiskUtil(ctx)

	if isISO {
		isoPath := filepath.Join(instDir, filenames.ISO)
		if err := os.Rename(imagePath, isoPath); err != nil {
			return err
		}
		f, err := os.Create(realDiskPath)
		if err != nil {
			_ = os.Rename(isoPath, imagePath)
			return err
		}
		if err := f.Close(); err != nil {
			os.Remove(realDiskPath)
			_ = os.Rename(isoPath, imagePath)
			return err
		}
		if err := diskUtil.Convert(ctx, diskImageFormat, realDiskPath, realDiskPath, &diskSizeInBytes, false); err != nil {
			os.Remove(realDiskPath)
			_ = os.Rename(isoPath, imagePath)
			return fmt.Errorf("failed to create disk %#q: %w", diskPath, err)
		}
	} else {
		format, err := nativeimgutil.DetectFormat(imagePath)
		if err != nil {
			return err
		}

		// Resize handled by prepareDisk() after CreateDisk(), except for a format
		// whose size go-qcow2reader cannot read: prepareDisk() skips it, so the
		// image keeps its own virtual size instead of diskSize.
		if format == string(diskImageFormat) {
			if diskImageFormat == vhdx.Type {
				logrus.Warnf("Disk size %#q is not applied to %#q: a %s image keeps the virtual size it was built with", diskSize, diskPath, diskImageFormat)
			}
			if err = os.Rename(imagePath, realDiskPath); err != nil {
				if !errors.Is(err, syscall.EXDEV) {
					return fmt.Errorf("failed to rename %#q to %#q: %w", imagePath, diskPath, err)
				}
				if err := contfs.CopyFile(imagePath, realDiskPath); err != nil {
					return fmt.Errorf("failed to copy %#q into %#q: %w", imagePath, realDiskPath, err)
				}
				if err := os.Remove(imagePath); err != nil {
					return err
				}
				os.Remove(imagePath)
			}
			if err := os.Chmod(realDiskPath, 0o644); err != nil {
				return fmt.Errorf("failed to chmod %#q: %w", diskPath, err)
			}
		} else {
			if err := diskUtil.Convert(ctx, diskImageFormat, imagePath, realDiskPath, &diskSizeInBytes, false); err != nil {
				return fmt.Errorf("failed to convert %#q to %#q: %w", imagePath, diskPath, err)
			}
			os.Remove(imagePath)
		}
	}
	return nil
}
