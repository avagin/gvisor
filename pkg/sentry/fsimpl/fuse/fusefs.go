// Copyright 2020 The gVisor Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package fuse implements fusefs.
package fuse

import (
	"strconv"

	"gvisor.dev/gvisor/pkg/abi/linux"
	"gvisor.dev/gvisor/pkg/context"
	"gvisor.dev/gvisor/pkg/log"
	"gvisor.dev/gvisor/pkg/sentry/fsimpl/kernfs"
	"gvisor.dev/gvisor/pkg/sentry/kernel"
	"gvisor.dev/gvisor/pkg/sentry/kernel/auth"
	"gvisor.dev/gvisor/pkg/sentry/vfs"
	"gvisor.dev/gvisor/pkg/syserror"
)

// Name is the default filesystem name.
const Name = "fuse"

// FilesystemType implements vfs.FilesystemType.
type FilesystemType struct{}

type filesystemOptions struct {
	// userID specifies the numeric uid of the mount owner.
	// This option should not be specified by the filesystem owner.
	// It is set by libfuse (or, if libfuse is not used, must be set
	// by the filesystem itself). For more information, see man page
	// for fuse(8)
	userID uint32

	// groupID specifies the numeric uid of the mount owner.
	// This option should not be specified by the filesystem owner.
	// It is set by libfuse (or, if libfuse is not used, must be set
	// by the filesystem itself). For more information, see man page
	// for fuse(8)
	groupID uint32

	// rootMode specifies the the file mode of the filesystem's root.
	rootMode linux.FileMode
}

// filesystem implements vfs.FilesystemImpl.
type filesystem struct {
	kernfs.Filesystem
	devMinor uint32

	// fuseConn is used for communication between the FUSE server
	// daemon and the sentry fusefs.
	fuseConn *Connection

	// opts is the options the fusefs is initialized with.
	opts filesystemOptions
}

// Name implements vfs.FilesystemType.Name.
func (FilesystemType) Name() string {
	return Name
}

// GetFilesystem implements vfs.FilesystemType.GetFilesystem.
func (fsType FilesystemType) GetFilesystem(ctx context.Context, vfsObj *vfs.VirtualFilesystem, creds *auth.Credentials, source string, opts vfs.GetFilesystemOptions) (*vfs.Filesystem, *vfs.Dentry, error) {
	devMinor, err := vfsObj.GetAnonBlockDevMinor()
	if err != nil {
		return nil, nil, err
	}

	var fsopts filesystemOptions
	mopts := vfs.GenericParseMountOptions(opts.Data)
	deviceDescriptorStr, ok := mopts["fd"]
	if !ok {
		log.Warningf("%s.GetFilesystem: communication file descriptor N (obtained by opening /dev/fuse) must be specified as 'fd=N'", fsType.Name())
		return nil, nil, syserror.EINVAL
	}
	delete(mopts, "fd")

	deviceDescriptor, err := strconv.ParseInt(deviceDescriptorStr, 10 /* base */, 32 /* bitSize */)
	if err != nil {
		return nil, nil, err
	}

	kernelTask := kernel.TaskFromContext(ctx)
	if kernelTask == nil {
		log.Warningf("%s.GetFilesystem: couldn't get kernel task from context", fsType.Name())
		return nil, nil, syserror.EINVAL
	}
	fuseFd := kernelTask.GetFileVFS2(int32(deviceDescriptor))

	// Parse and set all the other supported FUSE mount options.
	// TODO: Expand the supported mount options.
	if userIDStr, ok := mopts["user_id"]; ok {
		delete(mopts, "user_id")
		userID, err := strconv.ParseUint(userIDStr, 10, 32)
		if err != nil {
			log.Warningf("%s.GetFilesystem: invalid user_id: user_id=%s", fsType.Name(), userIDStr)
			return nil, nil, syserror.EINVAL
		}
		fsopts.userID = uint32(userID)
	}

	if groupIDStr, ok := mopts["group_id"]; ok {
		delete(mopts, "group_id")
		groupID, err := strconv.ParseUint(groupIDStr, 10, 32)
		if err != nil {
			log.Warningf("%s.GetFilesystem: invalid group_id: group_id=%s", fsType.Name(), groupIDStr)
			return nil, nil, syserror.EINVAL
		}
		fsopts.groupID = uint32(groupID)
	}

	rootMode := linux.FileMode(0777)
	modeStr, ok := mopts["rootmode"]
	if ok {
		delete(mopts, "rootmode")
		mode, err := strconv.ParseUint(modeStr, 8, 32)
		if err != nil {
			log.Warningf("%s.GetFilesystem: invalid mode: %q", fsType.Name(), modeStr)
			return nil, nil, syserror.EINVAL
		}
		rootMode = linux.FileMode(mode & 07777)
	}
	fsopts.rootMode = rootMode

	// Check for unparsed options.
	if len(mopts) != 0 {
		log.Warningf("%s.GetFilesystem: unknown options: %v", fsType.Name(), mopts)
		return nil, nil, syserror.EINVAL
	}

	// Create a new FUSE connection.
	fs, err := NewFUSEFilesystem(ctx, devMinor, fsopts, fuseFd)
	if err != nil {
		log.Warningf("%s.NewFUSEFilesystem: failed with error: %v", fsType.Name(), err)
		return nil, nil, err
	}

	fs.VFSFilesystem().Init(vfsObj, &fsType, fs)

	// Send a FUSE_INIT request to the FUSE daemon server before returning.
	// This call is not blocking.
	if err := fs.InitSend(creds, uint32(kernelTask.ThreadID())); err != nil {
		log.Warningf("%s.InitSend: failed with error: %v", fsType.Name(), err)
		return nil, nil, err
	}

	// root is the fusefs root directory.
	defaultFusefsDirMode := linux.FileMode(0755)
	root := fs.newInode(creds, defaultFusefsDirMode)

	return fs.VFSFilesystem(), root.VFSDentry(), nil
}

// NewFUSEFilesystem creates a new FUSE filesystem.
func NewFUSEFilesystem(ctx context.Context, devMinor uint32, opts filesystemOptions, device *vfs.FileDescription) (*filesystem, error) {
	fs := &filesystem{
		devMinor: devMinor,
		opts:     opts,
	}

	conn, err := NewFUSEConnection(ctx, device)
	if err != nil {
		log.Warningf("fuse.NewFUSEFilesystem: NewFUSEConnection failed with error: %v", err)
		return nil, syserror.EINVAL
	}

	fs.fuseConn = conn
	fuseFD := device.Impl().(*DeviceFD)
	fuseFD.fs = fs

	return fs, nil
}

// Release implements vfs.FilesystemImpl.Release.
func (fs *filesystem) Release() {
	fs.Filesystem.VFSFilesystem().VirtualFilesystem().PutAnonBlockDevMinor(fs.devMinor)
	fs.Filesystem.Release()
}

// Inode implements kernfs.Inode.
type Inode struct {
	// TODO: Change these directory inode implementations once fusefs is more fleshed out.
	kernfs.InodeAttrs
	kernfs.InodeNoDynamicLookup
	kernfs.InodeNotSymlink
	kernfs.InodeDirectoryNoNewChildren
	kernfs.OrderedChildren

	locks vfs.FileLocks

	dentry kernfs.Dentry
}

func (fs *filesystem) newInode(creds *auth.Credentials, mode linux.FileMode) *kernfs.Dentry {
	i := &Inode{}
	i.InodeAttrs.Init(creds, linux.UNNAMED_MAJOR, fs.devMinor, fs.NextIno(), linux.ModeDirectory|0755)
	i.OrderedChildren.Init(kernfs.OrderedChildrenOptions{})
	i.dentry.Init(i)

	return &i.dentry
}

// Open implements kernfs.Inode.Open.
func (i *Inode) Open(ctx context.Context, rp *vfs.ResolvingPath, vfsd *vfs.Dentry, opts vfs.OpenOptions) (*vfs.FileDescription, error) {
	fd, err := kernfs.NewGenericDirectoryFD(rp.Mount(), vfsd, &i.OrderedChildren, &i.locks, &opts)
	if err != nil {
		return nil, err
	}
	return fd.VFSFileDescription(), nil
}

// Stat implements kernfs.Inode.Stat.
func (i *Inode) Stat(ctx context.Context, fs *vfs.Filesystem, opts vfs.StatOptions) (linux.Statx, error) {
	fusefs := fs.Impl().(*filesystem)
	task, creds := kernel.TaskFromContext(ctx), auth.CredentialsFromContext(ctx)

	var in linux.FUSEGetAttrIn
	req, err := fusefs.fuseConn.NewRequest(creds, uint32(task.ThreadID()), i.Ino(), linux.FUSE_GETATTR, &in)
	if err != nil {
		return linux.Statx{}, nil
	}

	res, err := fusefs.fuseConn.Call(task, req)
	if err != nil {
		return linux.Statx{}, err
	}
	if err := res.Error(); err != nil {
		return linux.Statx{}, err
	}

	var out linux.FUSEGetAttrOut
	if err := res.UnmarshalPayload(&out); err != nil {
		return linux.Statx{}, err
	}

	var stat linux.Statx
	if opts.Mask&linux.STATX_MODE != 0 {
		stat.Mode = uint16(out.Attr.Mode)
	}
	if opts.Mask&linux.STATX_NLINK != 0 {
		stat.Nlink = out.Attr.Nlink
	}
	if opts.Mask&linux.STATX_UID != 0 {
		stat.UID = out.Attr.UID
	}
	if opts.Mask&linux.STATX_GID != 0 {
		stat.GID = out.Attr.GID
	}
	if opts.Mask&linux.STATX_ATIME != 0 {
		stat.Atime = linux.StatxTimestamp{
			Sec:  int64(out.Attr.Atime),
			Nsec: out.Attr.AtimeNsec,
		}
	}
	if opts.Mask&linux.STATX_MTIME != 0 {
		stat.Mtime = linux.StatxTimestamp{
			Sec:  int64(out.Attr.Mtime),
			Nsec: out.Attr.MtimeNsec,
		}
	}
	if opts.Mask&linux.STATX_CTIME != 0 {
		stat.Ctime = linux.StatxTimestamp{
			Sec:  int64(out.Attr.Ctime),
			Nsec: out.Attr.CtimeNsec,
		}
	}
	if opts.Mask&linux.STATX_INO != 0 {
		stat.Ino = out.Attr.Ino
	}
	if opts.Mask&linux.STATX_SIZE != 0 {
		stat.Size = out.Attr.Size
	}
	if opts.Mask&linux.STATX_BLOCKS != 0 {
		stat.Blocks = out.Attr.Blocks
	}
	stat.Blksize = out.Attr.BlkSize
	return stat, nil
}
