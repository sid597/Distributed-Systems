package kvraft

import (
	"bytes"
	"log"
	"sync"
	"sync/atomic"

	// "fmt"
	"math/rand"
	"time"

	"../labgob"
	"../labrpc"
	"../raft"
)

const Debug = 1

func Pf(format string, a ...interface{}) (n int, err error) {
	if Debug > 0 {
		log.Printf(format, a...)
	}
	return
}

type Op struct {
	// Your definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
	Key       string
	Value     string
	Type      string
	ClientId  int64
	RequestId int
}
type KVServer struct {
	mu      sync.Mutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg
	dead    int32 // set by Kill()

	maxraftstate int // snapshot if log grows this big

	// Your definitions here.
	persister        *raft.Persister
	resCh            chan raft.ApplyMsg
	db               map[string]string
	waiting          bool
	quit             chan bool
	previousRequests map[int64]raft.PreviousRequest
}

// Snapshot

func (kv *KVServer) Get(args *GetArgs, reply *GetReply) {
	// check the requestId for the corresponding client and if this requestId > table one
	// then execute else return the result from table itself
	kv.mu.Lock()
	if previous, ok := kv.previousRequests[args.ClientId]; ok {
		if args.RequestId <= previous.RequestId {
			// return value from previous request
			reply.Err = "Leader"
			reply.Value = previous.Result
			kv.mu.Unlock()
			return
		}
	}
	kv.mu.Unlock()

	index, _, isLeader := kv.rf.Start(Op{args.Key, "", "Get", args.ClientId, args.RequestId})

	startTime := time.Now()

	if !isLeader {
		reply.Err = "Not Leader"
		return
	} else {
		kv.mu.Lock()
		kv.waiting = true
		kv.mu.Unlock()
		Pf("[%v] GET request, Key: %v, RId : %v, for index %v", kv.me, args.Key, args.RequestId, index)
		for {
			Pf("[%v] GET NEW REQUEST with args %v", kv.me, args)
			select {
			case <-time.After(time.Millisecond * 600):
				kv.mu.Lock()
				kv.waiting = false
				Pf("[%v] Quitting, time since %v, args %v", kv.me, time.Now().Sub(startTime), args)
				reply.Err = "Timeout"
				kv.mu.Unlock()
				return
			case res := <-kv.resCh:
				Pf("[%v] GET RECEIVED ON CHANNEL, res is %v, args were %v", kv.me, res, &args)
				kv.mu.Lock()
				if res.CommandIndex == index {
					reply.Err = "Leader"
					reply.Value = kv.db[args.Key]
					Pf("[%v] Get replying %v, for index %v, with result %v", kv.me, reply, index, res)
					Pf("[%v] DB IS %v", kv.me, kv.db)
					kv.mu.Unlock()
					return
				}
				kv.mu.Unlock()
			}
		}
	}
}

// fmt.Printf("GET Request id is %v \n", args.RequestId)

func (kv *KVServer) PutAppend(args *PutAppendArgs, reply *PutAppendReply) {
	// check the requestId for the corresponding client and if this requestId > table one
	// then execute else return the result from table itself
	kv.mu.Lock()
	if previous, ok := kv.previousRequests[args.ClientId]; ok {
		if args.RequestId <= previous.RequestId {
			// return value from previous request
			reply.Err = "Leader"
			kv.mu.Unlock()
			return
		}
	}
	opType := "Append"
	if args.Op == "Put" {
		opType = "Put"
	}
	kv.mu.Unlock()
	index, _, isLeader := kv.rf.Start(Op{args.Key, args.Value, opType, args.ClientId, args.RequestId})

	startTime := time.Now()
	if !isLeader {
		reply.Err = "Not Leader"
		return
	} else {
		Pf("[%v] PA NEW REQUEST with args %v", kv.me, args)
		kv.mu.Lock()
		kv.waiting = true
		kv.mu.Unlock()
		for {
			select {
			case <-time.After(time.Millisecond * 600):
				Pf("[%v] Quitting, time since %v, args %v", kv.me, time.Now().Sub(startTime), args)
				reply.Err = "Timeout"
				kv.mu.Lock()
				kv.waiting = false
				kv.mu.Unlock()
				return
			case res := <-kv.resCh:
				kv.mu.Lock()
				if res.Command.(Op).ClientId != args.ClientId {
					reply.Err = "Timeout"
					kv.mu.Unlock()
					return
				}
				Pf("[%v] PA RECEIVED ON CHANNEL, for args %v, res %v, index %v", kv.me, args, res, index)
				if res.CommandIndex == index {
					reply.Err = "Leader"
					Pf("[%v] Result is %v, index is %v, reply %v", kv.me, res, index, reply)
					Pf("[%v] DB IS %v", kv.me, kv.db)
					kv.mu.Unlock()
					return
				}
				kv.mu.Unlock()
			}
		}
	}

}

//
// the tester calls Kill() when a KVServer instance won't
// be needed again. for your convenience, we supply
// code to set rf.dead (without needing a lock),
// and a killed() method to test rf.dead in
// long-running loops. you can also add your own
// code to Kill(). you're not required to do anything
// about this, but it may be convenient (for example)
// to suppress debug output from a Kill()ed instance.
//
func (kv *KVServer) Kill() {
	atomic.StoreInt32(&kv.dead, 1)
	kv.rf.Kill()
	// Your code here, if desired.
}

func (kv *KVServer) killed() bool {
	z := atomic.LoadInt32(&kv.dead)
	return z == 1
}

func (kv *KVServer) AlreadySeen(current Op) bool {

	clientId := current.ClientId
	requestId := current.RequestId

	if previous, ok := kv.previousRequests[clientId]; ok {
		Pf("[%v] Previous Request Id %v", kv.me, previous.RequestId)
		if previous.RequestId == requestId || previous.RequestId > requestId{
			return true
		}
		return false
	}
	return false

}

func (kv *KVServer) Receive() {
	Pf("[%v] Receiving", kv.me)
	for x := range kv.applyCh {
		Pf("[%v] x is %v", kv.me, x)

		if x.CommandValid {
			func() {
				kv.mu.Lock()
				waiting := kv.waiting
				alreadySeen := kv.AlreadySeen(x.Command.(Op))
				// Pf("[%v] Waiting %v", kv.me, waiting)
				kv.mu.Unlock()
				if waiting && !alreadySeen {
					Pf("[%v] Someone is waiting for reply %v ", kv.me, x)
					kv.resCh <- x
				}
				kv.mu.Lock()
				Pf("[%v] Operation is %v, current db state %v", kv.me, x.Command.(Op), kv.db)
				key := x.Command.(Op).Key
				value := x.Command.(Op).Value
				opType := x.Command.(Op).Type
				clientId := x.Command.(Op).ClientId
				requestId := x.Command.(Op).RequestId
				// Check if the result request is already seen
				// if not need to update previousRequest table also
				if !alreadySeen {
					if opType == "Put" {
						kv.db[key] = value
					} else if opType == "Append" {
						kv.db[key] += value
					}
					kv.previousRequests[clientId] = raft.PreviousRequest{requestId, kv.db[key]}
				}
				db := kv.db
				Pf("[%v] AFTER Opertation completion DB IS %v", kv.me, kv.db)
				kv.rf.PersistSnapshot(kv.previousRequests)
				if kv.maxraftstate != -1 && kv.persister.RaftStateSize() > kv.maxraftstate {
					w := new(bytes.Buffer)
					e := labgob.NewEncoder(w)
					e.Encode(db)
					db := w.Bytes()
					ri := rand.Intn(300)
					Pf("[%v] Sending command with ri %v", kv.me, ri)
					kv.mu.Unlock()
					go kv.rf.DiscardEntriesUpto(x.CommandIndex-1, db, ri)
					return
				}
				kv.mu.Unlock()
			}()
		} else {
			kv.mu.Lock()
			r := bytes.NewBuffer(x.Data)
			d := labgob.NewDecoder(r)
			var snap raft.Snapshot
			d.Decode(&snap)

			Pf("[%v] NEW DATA DARLINGS PR : %v, --- %v, data %v", kv.me,snap.PreviousRequests, snap, x.Data)

			if len(snap.PreviousRequests) > 0 {
				kv.previousRequests = snap.PreviousRequests
			}
			kv.db = snap.MachineState
			Pf("[%v] DB IS %v", kv.me, kv.db)
			kv.rf.PersistSnapshot(kv.previousRequests)
			kv.mu.Unlock()
		}

	}
}

//
// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant key/value service.
// me is the index of the current server in servers[].
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
// the k/v server should snapshot when Raft's saved state exceeds maxraftstate bytes,
// in order to allow Raft to garbage-collect its log. if maxraftstate is -1,
// you don't need to snapshot.
// StartKVServer() must return quickly, so it should start goroutines
// for any long-running work.
//
func StartKVServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister, maxraftstate int) *KVServer {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(Op{})

	kv := new(KVServer)
	kv.me = me
	kv.maxraftstate = maxraftstate

	// You may need initialization code here.

	kv.applyCh = make(chan raft.ApplyMsg)
	kv.resCh = make(chan raft.ApplyMsg)
	kv.quit = make(chan bool)

	// You may need initialization code here.
	kv.db = make(map[string]string)
	kv.previousRequests = make(map[int64]raft.PreviousRequest)
	kv.persister = persister
	go kv.Receive()

	kv.mu.Lock()
	kv.rf = raft.Make(servers, me, persister, kv.applyCh)
	kv.mu.Unlock()
	return kv
}
