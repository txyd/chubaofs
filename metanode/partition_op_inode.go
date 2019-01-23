// Copyright 2018 The Containerfs Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.

package metanode

import (
	"encoding/json"
	"time"

	"github.com/tiglabs/containerfs/proto"
)

func replyInfo(info *proto.InodeInfo, ino *Inode) bool {
	ino.RLock()
	if ino.Flag&DeleteMarkFlag > 0 {
		return false
	}
	info.Inode = ino.Inode
	info.Mode = ino.Type
	info.Size = ino.Size
	info.Nlink = ino.NLink
	info.Uid = ino.Uid
	info.Gid = ino.Gid
	info.Generation = ino.Generation
	if length := len(ino.LinkTarget); length > 0 {
		info.Target = make([]byte, length)
		copy(info.Target, ino.LinkTarget)
	}
	info.CreateTime = time.Unix(ino.CreateTime, 0)
	info.AccessTime = time.Unix(ino.AccessTime, 0)
	info.ModifyTime = time.Unix(ino.ModifyTime, 0)
	ino.RUnlock()
	return true
}

func (mp *metaPartition) CreateInode(req *CreateInoReq, p *Packet) (err error) {
	inoID, err := mp.nextInodeID()
	if err != nil {
		p.PacketErrorWithBody(proto.OpInodeFullErr, []byte(err.Error()))
		return
	}
	ino := NewInode(inoID, req.Mode)
	ino.LinkTarget = req.Target
	val, err := ino.Marshal()
	if err != nil {
		p.PacketErrorWithBody(proto.OpErr, []byte(err.Error()))
		return
	}
	resp, err := mp.Put(opFSMCreateInode, val)
	if err != nil {
		p.PacketErrorWithBody(proto.OpAgain, []byte(err.Error()))
		return
	}
	var (
		status = proto.OpNotExistErr
		reply  []byte
	)
	if resp.(uint8) == proto.OpOk {
		resp := &CreateInoResp{
			Info: &proto.InodeInfo{},
		}
		if replyInfo(resp.Info, ino) {
			status = proto.OpOk
			reply, err = json.Marshal(resp)
			if err != nil {
				status = proto.OpErr
				reply = []byte(err.Error())
			}
		}
	}
	p.PacketErrorWithBody(status, reply)
	return
}

func (mp *metaPartition) UnlinkInode(req *UnlinkInoReq, p *Packet) (err error) {
	ino := NewInode(req.Inode, 0)
	val, err := ino.Marshal()
	if err != nil {
		p.PacketErrorWithBody(proto.OpErr, nil)
		return
	}
	r, err := mp.Put(opFSMUnlinkInode, val)
	if err != nil {
		p.PacketErrorWithBody(proto.OpAgain, []byte(err.Error()))
		return
	}
	msg := r.(*ResponseInode)
	status := msg.Status
	var reply []byte
	if status == proto.OpOk {
		resp := &UnlinkInoResp{
			Info: &proto.InodeInfo{},
		}
		replyInfo(resp.Info, msg.Msg)
		if reply, err = json.Marshal(resp); err != nil {
			status = proto.OpErr
		}
	}
	p.PacketErrorWithBody(status, reply)
	return
}

func (mp *metaPartition) Open(req *OpenReq, p *Packet) (err error) {
	req.ATime = Now.GetCurrentTime().Unix()
	val, err := json.Marshal(req)
	if err != nil {
		p.PacketErrorWithBody(proto.OpErr, nil)
		return
	}
	resp, err := mp.Put(opFSMOpen, val)
	if err != nil {
		p.PacketErrorWithBody(proto.OpAgain, []byte(err.Error()))
		return
	}
	var reply []byte
	msg := resp.(*ResponseInode)
	if reply, err = json.Marshal(&proto.OpenResponse{
		AuthID: msg.AuthID,
	}); err != nil {
		p.PacketErrorWithBody(proto.OpErr, nil)
		return
	}
	p.PacketErrorWithBody(msg.Status, reply)
	return
}

func (mp *metaPartition) ReleaseOpen(req *ReleaseReq, p *Packet) (err error) {
	ino := NewInode(req.Inode, 0)
	ino.AuthID = req.AuthID
	val, err := ino.Marshal()
	if err != nil {
		p.PacketErrorWithBody(proto.OpErr, []byte(err.Error()))
		return
	}
	r, err := mp.Put(opFSMReleaseOpen, val)
	if err != nil {
		p.PacketErrorWithBody(proto.OpAgain, []byte(err.Error()))
		return
	}
	p.PacketErrorWithBody(r.(uint8), nil)
	return
}

func (mp *metaPartition) InodeGet(req *InodeGetReq, p *Packet) (err error) {
	ino := NewInode(req.Inode, 0)
	retMsg := mp.getInode(ino)
	ino = retMsg.Msg
	var (
		reply  []byte
		status = proto.OpNotExistErr
	)
	if retMsg.Status == proto.OpOk {
		resp := &proto.InodeGetResponse{
			Info: &proto.InodeInfo{},
		}
		if replyInfo(resp.Info, retMsg.Msg) {
			status = proto.OpOk
			reply, err = json.Marshal(resp)
			if err != nil {
				status = proto.OpErr
			}
		}
	}
	p.PacketErrorWithBody(status, reply)
	return
}

func (mp *metaPartition) InodeGetBatch(req *InodeGetReqBatch, p *Packet) (err error) {
	resp := &proto.BatchInodeGetResponse{}
	ino := NewInode(0, 0)
	for _, inoId := range req.Inodes {
		ino.Inode = inoId
		retMsg := mp.getInode(ino)
		if retMsg.Status == proto.OpOk {
			inoInfo := &proto.InodeInfo{}
			if replyInfo(inoInfo, retMsg.Msg) {
				resp.Infos = append(resp.Infos, inoInfo)
			}
		}
	}
	data, err := json.Marshal(resp)
	if err != nil {
		p.PacketErrorWithBody(proto.OpErr, nil)
		return
	}
	p.PacketOkWithBody(data)
	return
}

func (mp *metaPartition) InodeGetAuth(ino uint64, p *Packet) (err error) {
	resp := mp.getInode(NewInode(ino, 0))
	status := resp.Status
	if status != proto.OpOk {
		p.PacketErrorWithBody(status, nil)
		return
	}
	authID, timeout := resp.Msg.GetAuth()
	data, err := json.Marshal(map[string]interface{}{
		"authID":   authID,
		"authTime": timeout,
	})
	if err != nil {
		status = proto.OpErr
	}
	p.PacketErrorWithBody(status, data)
	return
}

func (mp *metaPartition) CreateLinkInode(req *LinkInodeReq, p *Packet) (err error) {
	ino := NewInode(req.Inode, 0)
	val, err := ino.Marshal()
	if err != nil {
		p.PacketErrorWithBody(proto.OpErr, []byte(err.Error()))
		return
	}
	resp, err := mp.Put(opFSMCreateLinkInode, val)
	if err != nil {
		p.PacketErrorWithBody(proto.OpAgain, []byte(err.Error()))
		return
	}
	retMsg := resp.(*ResponseInode)
	status := proto.OpNotExistErr
	var reply []byte
	if retMsg.Status == proto.OpOk {
		resp := &LinkInodeResp{
			Info: &proto.InodeInfo{},
		}
		if replyInfo(resp.Info, retMsg.Msg) {
			status = proto.OpOk
			reply, err = json.Marshal(resp)
			if err != nil {
				status = proto.OpErr
			}
		}

	}
	p.PacketErrorWithBody(status, reply)
	return
}

func (mp *metaPartition) EvictInode(req *EvictInodeReq, p *Packet) (err error) {
	ino := NewInode(req.Inode, 0)
	val, err := ino.Marshal()
	if err != nil {
		p.PacketErrorWithBody(proto.OpErr, []byte(err.Error()))
		return
	}
	resp, err := mp.Put(opFSMEvictInode, val)
	if err != nil {
		p.PacketErrorWithBody(proto.OpAgain, []byte(err.Error()))
		return
	}
	msg := resp.(*ResponseInode)
	p.PacketErrorWithBody(msg.Status, nil)
	return
}

func (mp *metaPartition) SetAttr(reqData []byte, p *Packet) (err error) {
	_, err = mp.Put(opFSMSetAttr, reqData)
	if err != nil {
		p.PacketErrorWithBody(proto.OpAgain, []byte(err.Error()))
		return
	}
	p.PacketOkReply()
	return
}

func (mp *metaPartition) GetInodeTree() *BTree {
	return mp.inodeTree.GetTree()
}
