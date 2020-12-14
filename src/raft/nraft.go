package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	"bytes"
	// "fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"../labgob"
	"../labrpc"
)


//
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in Lab 3 you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh; at that point you can add fields to
// ApplyMsg, but set CommandValid to false for these other uses.
//
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int
	IsLeader bool
}

//
// A Go object implementing a single Raft peer.
//
type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	mu0       sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what state a Raft server must maintain.

	// Lab 2A

	raftId        int
	state         State
	lastReceived  time.Time
	electionAlarm time.Duration

	currentTerm int
	votedFor    int

	totalServers int

	// Lab 2B

	applyCh     chan ApplyMsg
	log         []LogEntry
	commitIndex int
	lastApplied int
	index       int

	lastAppliedRpc time.Time
	NextIndex      []int
	MatchIndex     []int
}

type LogEntry struct {
	Term    int
	Command interface{}
}

type State string

const (
	Follower  = "Follower"
	Leader    = "Leader"
	Candidate = "Candidate"
)

func (rf *Raft) BecomeFollower() {
	if rf.state != Follower {
		Pf("[%v] Asked to ____BECOME FOLLOWER for term [%v] ", rf.me, rf.currentTerm)
		rf.state = Follower
		rf.ResetElectionAlarm()
	}
}

func (rf *Raft) BecomeLeader() {

	rf.mu.Lock()
	if rf.state != Leader {
		Pf("[%v] Asked to ____BECOME LEADER______ for term [%v] ", rf.me, rf.currentTerm)
		rf.state = Leader
	}
	rf.lastAppliedRpc = time.Now()
	rf.NextIndex = []int{}
	rf.MatchIndex = []int{}
	ri := rand.Intn(5000)
	me := rf.me

	for i := 0; i < rf.totalServers; i++ {
		rf.NextIndex = append(rf.NextIndex, len(rf.log))
		if i == me {
			rf.MatchIndex = append(rf.MatchIndex, len(rf.log)-1)
		} else {
			rf.MatchIndex = append(rf.MatchIndex, 0)
		}
	}
	Pf("[%v] NextIndex and MatchIndex are : %v, %v", rf.me, rf.NextIndex, rf.MatchIndex)
	rf.mu.Unlock()
	Pf("[%v] %v LEADER 1st Heartbeat ", me, ri)
	rf.StartAgreement(ri)

	for !rf.killed() {

		time.Sleep(11 * time.Millisecond)
		//rf.CheckCo1mitIndex()

		rf.mu.Lock()
		timeSince := time.Now().Sub(rf.lastAppliedRpc)
		curState := rf.state
		ri = rand.Intn(5000)

		if curState != Leader {
			Pf("[%v] =============== NOT LEADER ANYMORE ========== %v = %v ===", rf.me, rf.state, rf.currentTerm)
			rf.mu.Unlock()
			return
		}
		if timeSince > 100*time.Millisecond {
			Pf("")
			Pf("[%v] %v TIME SINCE : %v, So Sending Heartbeat ", me, ri, timeSince)
			////.Printf("[%v] %v TIME SINCE : %v, So Sending Heartbeat \n", me, ri, timeSince)
			rf.lastAppliedRpc = time.Now()
			rf.mu.Unlock()
			rf.StartAgreement(ri)
		} else {
			rf.mu.Unlock()
		}
	}
}

func (rf *Raft) BecomeCandidate() {

	rf.mu.Lock()

	Pf("[%v] Asked to _____BECOME CANDIDATE_____ for term [%v] ", rf.me, rf.currentTerm+1)
	rf.state = Candidate
	rf.mu.Unlock()
	rf.NewElection()
}

////////////////////////////////////////////////////////////////////////////////
// Resetting election
////////////////////////////////////////////////////////////////////////////////

//
// Set election time between 250-500 milliseconds
//
func (rf *Raft) ResetElectionAlarm() {
	rf.lastReceived = time.Now()
	rf.electionAlarm = time.Duration(rand.Intn(250)+250) * time.Millisecond
	Pf("[%v] Election alarm reset to : [%v] for term [%v]", rf.me, rf.electionAlarm, rf.currentTerm)
}

/*
	Run election timer in follower and candidate state to check if its time
	for another election

	This will be long running thread
*/

func (rf *Raft) StartElectionCountdown() {

	for !rf.killed() {

		time.Sleep(13 * time.Millisecond)

		rf.mu.Lock()

		// timeSince := time.Now().Sub(rf.lastAppliedRpc)
		// Pf("[%v]  Time since %v", rf.me, timeSince)
		if rf.state == Leader {

			// Somehow stop this thread / set election alarm infinite
			rf.electionAlarm = 20 * time.Second
			rf.mu.Unlock()
		} else {
			timeElapsed := time.Now().Sub(rf.lastReceived)
			if timeElapsed > rf.electionAlarm {
				//Pf("[%v] timeout after [%v] was expected [%v] current state [%v] for term [%v]", me, timeElapsed, electionAlarm, state, term)
				rf.mu.Unlock()
				rf.BecomeCandidate()
			} else {
				rf.mu.Unlock()
			}
		}
	}
}

////////////////////////////////////////////////////////////////////////////////
// Election
////////////////////////////////////////////////////////////////////////////////

func (rf *Raft) NewElection() {

	rf.mu.Lock()
	defer rf.mu.Unlock()

	rnd := rand.Intn(8001) + 2000
	rf.currentTerm += 1
	Pf("[%v] %v New election for term [%v] ", rf.me, rnd, rf.currentTerm)

	rf.votedFor = rf.me
	me := rf.me
	forTerm := rf.currentTerm

	rf.ResetElectionAlarm()

	totalVotes := 1 // Voted for self
	rf.persist()

	for server, _ := range rf.peers {
		if server != me {

			args := RequestVoteArgs{}
			args.Term = forTerm
			args.CandidateId = rf.me
			args.LastLogIndex, args.LastLogTerm = rf.GetLastLogData()
			args.Rnd = rnd
			Pf("[%v] args for Getting Vote %v", rf.me, args)

			reply := RequestVoteReply{}

			Pf("[%v] Get vote from [%v] for term [%v] ", rf.me, server, rf.currentTerm)
			//.Printf("[%v] Get vote from [%v] for term [%v] \n", rf.me, server, rf.currentTerm)

			go func(server int, forTerm int) {

				ok := rf.sendRequestVote(server, &args, &reply)

				rf.mu.Lock()

				if ok && forTerm == rf.currentTerm && rf.state == Candidate {
					if reply.VoteGranted {

						totalVotes += 1
						majorityServers := rf.totalServers/2 + 1

						Pf("[%v] %v vote from [%v] result [%v] now Total Votes [%v] out of [%v] for Term : [%v]", rf.me, rnd, server, reply.VoteGranted, totalVotes, majorityServers, rf.currentTerm)

						if totalVotes >= majorityServers && rf.state != Leader {

							rf.mu.Unlock()
							rf.BecomeLeader()
							return
						}

						rf.mu.Unlock()
					} else {
						if reply.Term > rf.currentTerm {

							Pf("[%v] VOTER Term greater than Candidate Term [%v] ", rf.me, rf.currentTerm)
							rf.currentTerm = reply.Term
							rf.BecomeFollower()
							rf.persist()
							rf.mu.Unlock()

							return
						}
						rf.mu.Unlock()
					}
				} else {
					rf.mu.Unlock()
					return
				}
			}(server, forTerm)
		}
	}
	return
}

type RequestVoteArgs struct {
	// Your data here (2A, 2B).

	// For 2A
	Term        int
	CandidateId int

	// For 2B
	LastLogIndex int
	LastLogTerm  int

	// For debugging
	Rnd int
}

//
type RequestVoteReply struct {
	// Your data here (2A).
	Term        int
	VoteGranted bool
}

//
// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
//
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

func (rf *Raft) GetLastLogData() (int, int) {
	index := len(rf.log) - 1
	term := rf.log[index].Term
	return index, term
}

func (rf *Raft) IsMoreUptoDate(args *RequestVoteArgs) bool {
	lastEntryIndex, lastLogTerm := rf.GetLastLogData()
	// If the logs have last entry with different terms then the log with later term is
	// more upto date.
	// If logs end with the same term then whichever log is longer is more upto date
	Pf("[%v] lastLogTerm : %v, args LastlogTerm %v; lastEntryIndex %v, args lastEntryIndex %v", rf.me, lastLogTerm, args.LastLogTerm, lastEntryIndex, args.LastLogIndex)
	if args.LastLogTerm == lastLogTerm {
		return args.LastLogIndex >= lastEntryIndex
	} else {
		return args.LastLogTerm > lastLogTerm
	}
}

func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).

	//rf.ResetElectionAlarm() // This is a **HUGE** mistake DO NOT NEED TO RESET ELECTION TIMER ON EVERY REQUEST VOTE
	// THIS NEEDS TO BE DONE ONLY IF VOTE IS GRANTED

	rf.mu.Lock()
	defer rf.mu.Unlock()

	isMoreUptoDate := rf.IsMoreUptoDate(args)

	Pf("[%v] %v REQUEST RPC more up-to-date ? %v ", rf.me, args.Rnd, isMoreUptoDate)
	Pf("[%v] %v Vote requested by [%v], for term [%v], current Term [%v], Voted For [%v] ", rf.me, args.Rnd, args.CandidateId, args.Term, rf.currentTerm, rf.votedFor)

	// Only vote for the candidate if for this term server did not grant vote to someone else or the candidate itself
	reply.Term = rf.currentTerm
	if args.Term < rf.currentTerm {

		reply.VoteGranted = false

	} else if args.Term == rf.currentTerm {
		if (rf.votedFor == rf.totalServers+1 || rf.votedFor == args.CandidateId) && isMoreUptoDate {
			reply.VoteGranted = true
			rf.votedFor = args.CandidateId
			rf.ResetElectionAlarm()
			rf.persist()
		}
	} else {
		if isMoreUptoDate {
			reply.VoteGranted = true
			rf.votedFor = args.CandidateId
			rf.ResetElectionAlarm()
		}
		rf.currentTerm = args.Term
		rf.persist()
		rf.BecomeFollower()
	}
	Pf("[%v] %v reply to [%v]  is : %v, %v", rf.me, args.Rnd, args.CandidateId, reply.Term, reply.VoteGranted)
}

/////////////////////////////////////////////////////////////////
// Start Agreement
/////////////////////////////////////////////////////////////////

//
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
//

func (rf *Raft) CheckCommitIndex() {
	// This is related to leader state
	// If for some N > commitIndex, and a majority of MatchIndex[server] >= N && log[N] == currentTerm, set commit index to N
	// This is the way a leader increases its commit index, the reasoning for this is to stop the Figure 8 case in paper

	Pf("[%v] Checking Commit Index, match Indexes %v", rf.me, rf.MatchIndex)
	//defer rf.ApplyCommit()
	for _, N := range rf.MatchIndex {
		if N > rf.commitIndex {
			count := 0
			for _, N0 := range rf.MatchIndex {
				//Pf("[%v] log len Log is %v, N : %v, N0 : %v", rf.me, rf.log, N, N0)
				if N0 >= N && rf.log[N].Term == rf.currentTerm {
					count += 1
				}
			}
			Pf("[%v] Count for %v is %v", rf.me, N, count)
			if count > (rf.totalServers / 2) {
				rf.commitIndex = N
				Pf("[%v] COMMIT INDEX SET TO %v, Commit Index arr %v ", rf.me, rf.commitIndex, rf.MatchIndex)
				return
			}
		}
	}
	return
}

func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := -1
	term := -1
	isLeader := true

	// Your code here (2B).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	Pf("[%v]        START        ", rf.me)
	Pf("[%v] -------- command received : %v state : %v, term : %v , log len : %v, log : %v", rf.me, command, rf.state, rf.currentTerm, len(rf.log), rf.log)
	if rf.state == Leader {
		// Index at which need to append entry
		index = rf.NextIndex[rf.me]
		term = rf.currentTerm
		forEntry := LogEntry{Term: term, Command: command}
		rf.log = append(rf.log, forEntry)
		rf.persist()
		Pf("[%v] NEW ENTRY APPENDED for Index : %v,  term %v, forEntry %v, nextIndex : %v, matchIndex : %v, log len : %v, log is : %v", rf.me, index, term, forEntry, rf.NextIndex, rf.MatchIndex, len(rf.log), rf.log)
		rf.MatchIndex[rf.me]++
		rf.NextIndex[rf.me] = rf.MatchIndex[rf.me] + 1
		rf.lastAppliedRpc = time.Now()
	} else {
		isLeader = false
	}

	Pf("[%v] START command received, result : %v, %v, %v", rf.me, index, term, isLeader)

	return index, term, isLeader
}

func (rf *Raft) GetEntries(afterIndex int, forServer int, ri int) []LogEntry {

	Pf("[%v] %v Get entries for server %v, after %v", rf.me, ri, forServer, afterIndex)
	// Get all the entries afterIndex (including afterIndex)
	entries := []LogEntry{}
	for _, entry := range rf.log[afterIndex:] {
		entries = append(entries, entry)
	}
	return entries
}

func (rf *Raft) StartAgreement(ri int) {

	rf.mu.Lock()

	Pf("[%v] %v  START AGREEMENT         ", rf.me, ri)
	forIndex := len(rf.log) - 1 // Because we appended the new entry
	Pf("[%v] %v Start Agreement for Index %v", rf.me, ri, forIndex)

	forTerm := rf.currentTerm
	if rf.state != Leader {
		rf.mu.Unlock()
		return
	}

	rf.mu.Unlock()
	for server, _ := range rf.peers {
		if server != rf.me {
			//rf.mu.Lock()
			args := &AppendEntriesArgs{}

			rf.mu.Lock()
			nextIndex := rf.NextIndex[server]
			prevLogIndex := nextIndex - 1
			//Pf("[%v] %v NextIndex : %v, prevLogIndex: %v, commitIndex: %v, NextIndexes: %v, log is %v", rf.me, ri, nextIndex, prevLogIndex, rf.commitIndex, rf.NextIndex, rf.log)

			entries := []LogEntry{}
			if rf.NextIndex[server] < rf.NextIndex[rf.me] {
				entries = rf.GetEntries(rf.NextIndex[server], server, ri)
			}
			Pf("[%v] %v Next Index : %v, for server :%v, entries : %v ", rf.me, ri, rf.NextIndex[server], server, entries)
			args.Term = forTerm
			args.LeaderId = rf.me
			args.PrevLogIndex = prevLogIndex
			args.PrevLogTerm = rf.log[prevLogIndex].Term
			args.Entries = entries
			args.LeaderCommit = rf.commitIndex
			args.Ri = rand.Intn(5000)
			args.Mri = ri
			rf.mu.Unlock()

			Pf("[%v] %v SendingAppendEntries to server [%v] with arguments [%v] for Term [%v] ", rf.me, ri, server, args, args.Term)

			go rf.SendAppendEntryRPC(server, args, &AppendEntriesReply{})
		}
	}

	Pf("``````````````````````````````````````````````````````````````````````````````````````````````````````````````````")
	Pf("``````````````````````````````````````````````````````````````````````````````````````````````````````````````````")
	Pf("``````````````````````````````````````````````````````````````````````````````````````````````````````````````````")
	Pf("")
	return
}

type AppendEntriesArgs struct {
	// Your data here (2A, 2B).

	// For 2A
	Term     int
	LeaderId int

	// For 2B
	// ForIndex     int
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []LogEntry
	LeaderCommit int
	Ri           int
	Mri          int
}

func (rf *Raft) ApplyCommit() {
	for !rf.killed() {
		time.Sleep(17 * time.Millisecond)

		applyEntries := []LogEntry{}
		rf.mu.Lock()
		fromIndex := rf.lastApplied
		leader := rf.state == Leader
		rf.mu.Unlock()
		func() {
			rf.mu.Lock()
			defer rf.mu.Unlock()

			for rf.commitIndex > rf.lastApplied {

				Pf("[%v] APPLY COMMIT  last Applied:  %v, commit Index is : %v ", rf.me, rf.lastApplied, rf.commitIndex)
				Pf("[%v] APPLYING MESSAGE , lastApplied : %v, log Len %v, for log %v", rf.me, rf.lastApplied, len(rf.log), rf.log)
				// fmt.Printf("[%v] APPLYING MESSAGE , lastApplied : %v, log Len %v, for log %v \n", rf.me, rf.lastApplied, len(rf.log), rf.log)
				applyEntries = rf.log[rf.lastApplied+1 : rf.commitIndex+1]
				fromIndex = rf.lastApplied
				rf.lastApplied = rf.commitIndex

			}
		}()

		for i, entry := range applyEntries {
			newMsg := ApplyMsg{CommandValid: true, Command: entry.Command, CommandIndex: fromIndex + i + 1, IsLeader: leader}
			rf.applyCh <- newMsg
		}
	}
}

//
type AppendEntriesReply struct {
	// Your data here (2A).
	Term    int
	Success bool

	// Fast roll back
	// Will include extra details :
	//  XTerm( term in the conflicting entry (none for Case 3)), XIndex( Index of the first entry with that term (none for case 3)), XLen( log length)
	// Examples :
	// Case 1 ->   S1 : 4 5 5
	// 			   S2 : 4 6 6 6
	// Leader does not have XTerm, so nextIndex = XIndex
	//
	// Case 2 ->   S1 : 4 4 4
	// 			   S2 : 4 6 6 6
	// Leader does has XTerm, so nextIndex = leader's last entry for Xterm
	//
	// Case 3 ->   S1 : 4
	// 			   S2 : 4 6 6 6
	// Follower's log is too short, so nextIndex = XLen

	XTerm  int
	XIndex int
	XLen   int
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {

	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.ResetElectionAlarm()
	reply.Success = false
	currentTerm := rf.currentTerm
	reply.Term = currentTerm
	lastEntryIndex, _ := rf.GetLastLogData()
	Pf("[%v] %v %v args Term : %v, current term : %v, , prevLogIndex : %v, lastEntryIndex %v, log len %v ,args Entries : %v,log : %v", rf.me, args.Mri, args.Ri, args.Term, rf.currentTerm, args.PrevLogIndex, lastEntryIndex, len(rf.log), args.Entries, rf.log)

	if args.Term < rf.currentTerm {
		return
	}

	// log too short
	if lastEntryIndex < args.PrevLogIndex {
		reply.XLen = len(rf.log)
		return
	} else {
		followersTerm := rf.log[args.PrevLogIndex].Term
		if followersTerm != args.PrevLogTerm {
			reply.XTerm = followersTerm
			// find the first index for this term in followers log
			for i := args.PrevLogIndex; i > 0; i-- {
				if rf.log[i].Term != followersTerm {
					reply.XIndex = i + 1
					break
				}
			}
			Pf("[%v] %v %v Xindex %v, XTerm %v, Xlen %v, for index %v and Term %v", rf.me, args.Mri, args.Ri, reply.XIndex, reply.XTerm, reply.XLen, args.PrevLogIndex, followersTerm)
			return
		}
	}

	if args.Term > rf.currentTerm {
		rf.currentTerm = args.Term
		rf.persist()
		rf.BecomeFollower()
	}
	if rf.state == Candidate {
		rf.BecomeFollower()
	}

	// If existing entry conflicts with a new one (same index but different terms) delete the existing entry and all that follow it
	/*
		example 1: follower log :   [(0,0), (2,101), (2, 103), (2,104), (2,105)]
					Leader Request : PI: 2, PT: 2, entries: [(2,104), (2,105)]

		example 2: follower log :   [(0,0), (2,101), (2, 103), (2,104), (2,105)]
					Leader Request : PI: 3, PT: 2, entries: [(2,107) ]
	*/

	Pf("[%v] %v %v Current log Len : %v, entries len : %v, applied index : %v, prev log index : %v", rf.me, args.Mri, args.Ri, len(rf.log), len(args.Entries), rf.lastApplied, args.PrevLogIndex)
	appendAfterLogIndex := args.PrevLogIndex + 1
	appendAfterEntryIndex := 0

	for _, entry := range args.Entries {
		if appendAfterLogIndex > lastEntryIndex {
			break
		} else {
			if rf.log[appendAfterLogIndex].Term != entry.Term {
				break
			}
		}
		appendAfterLogIndex++
		appendAfterEntryIndex++
	}

	Pf("[%v] %v %v appendAfterLogIndex %v, appendAfterEntryIndex %v", rf.me, args.Mri, args.Ri, appendAfterLogIndex, appendAfterEntryIndex)

	//if appendAfterLogIndex != lastEntryIndex + 1 {
	if len(args.Entries) > 0 && appendAfterEntryIndex != len(args.Entries) {
		rf.log = append(rf.log[:appendAfterLogIndex], args.Entries[appendAfterEntryIndex:]...)
	}
	//}
	Pf("[%v] %v %v New log len %v, is %v ", rf.me, args.Mri, args.Ri, len(rf.log), rf.log)
	//fmt.Printf("[%v] %v %v New log len %v, is %v \n ", rf.me, args.Mri, args.Ri, len(rf.log), rf.log)

	Pf("[%v] %v %v Leader commit > commit Index : %v ", rf.me, args.Mri, args.Ri, args.LeaderCommit > rf.commitIndex)

	// if leader commit > commitIndex ; commitIndex = min (leader commit, index of last new entry)
	if args.LeaderCommit > rf.commitIndex {
		if args.LeaderCommit < len(rf.log) {
			rf.commitIndex = args.LeaderCommit
		} else {
			rf.commitIndex = len(rf.log)
		}
		//rf.ApplyCommit()
	}

	reply.Success = true
	rf.persist()
	return

}

func (rf *Raft) SendAppendEntryRPC(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)

	rf.mu.Lock()
	defer rf.mu.Unlock()

	if ok && rf.currentTerm == args.Term {
		if reply.Success {
			if rf.MatchIndex[server] < args.PrevLogIndex+len(args.Entries) {
				rf.MatchIndex[server] = args.PrevLogIndex + len(args.Entries)
				rf.NextIndex[server] = rf.MatchIndex[server] + 1
			}
			Pf("[%v] %v SUCCESS FROM SERVER %v [%v]", rf.me, args.Mri, server, args.Ri)
			Pf("[%v] %v MATCH INDEX is %v, NEXT INDEX is %v ", rf.me, args.Mri, rf.MatchIndex, rf.NextIndex)
			rf.CheckCommitIndex()
		} else {
			Pf("[%v] %v %v Result from server %v  is FALSE", rf.me, args.Mri, args.Ri, server)
			if reply.Term > args.Term {
				Pf("[%v] %v %v Follower (%v) Term > Leader Term ", rf.me, args.Mri, args.Ri, reply.Term)
				rf.currentTerm = reply.Term
				rf.persist()
				rf.BecomeFollower()

				return ok

			} else {

				// rf.MakeItTrue(server, ri)
				if reply.XLen != 0 {
					rf.NextIndex[server] = reply.XLen
				} else {
					XtermInLeader := false
					LastIndexWithXterm := -1
					for i := len(rf.log) - 1; i > 0; i-- {
						if rf.log[i].Term == reply.XTerm {
							LastIndexWithXterm = i
							XtermInLeader = true
							break
						}
					}
					if XtermInLeader {
						rf.NextIndex[server] = LastIndexWithXterm + 1
					} else {
						rf.NextIndex[server] = reply.XIndex
					}
				}
				Pf("[%v] %v NextIndex are %v for server %v ", rf.me, args.Mri, rf.NextIndex, server)
				return ok
			}
		}
	}
	return ok
}

////////////////////////////////////////////////////////////////////////////////
// Make
////////////////////////////////////////////////////////////////////////////////
//
// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
//
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me
	rf.applyCh = applyCh

	rf.raftId = rand.Intn(5000) // Created for debugging purposes

	Pf("[%v] Bought to life with raftId : [%v]", rf.me, rf.raftId)
	// Your initialization code here (2A, 2B, 2C).

	// For 2A  only

	rf.totalServers = len(peers)
	go rf.StartElectionCountdown()
	go rf.ApplyCommit()
	// initialize from state persisted before a crash
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.readPersist(persister.ReadRaftState())
	rf.BecomeFollower()

	return rf
}

////////////////////////////////////////////////////////////////////////////////
// Lab 2B - 2C
////////////////////////////////////////////////////////////////////////////////

func (rf *Raft) GetState() (int, bool) {

	var term int
	var isleader bool
	// Your code here (2A).
	rf.mu.Lock()

	Pf("0000000000000000000000000000000000000000")
	Pf("[%v] Asking State, current state is [%v] and term is [%v] with raftId [%v],log len %v, log is : %v", rf.me, rf.state, rf.currentTerm, rf.raftId, len(rf.log), rf.log)
	Pf("[%v]  Time since last RPC [%v] was expected [%v] current state [%v] ", rf.me, time.Now().Sub(rf.lastReceived), rf.electionAlarm, rf.state)
	Pf("0000000000000000000000000000000000000000")

	term = rf.currentTerm
	if rf.state == Leader {
		isleader = true
	} else {
		isleader = false
	}
	rf.mu.Unlock()

	return term, isleader
}

//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(rf.currentTerm)
	e.Encode(rf.votedFor)
	e.Encode(rf.log)
	data := w.Bytes()
	rf.persister.SaveRaftState(data)
}

//
// restore previously persisted state.
//
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		rf.currentTerm = 1
		rf.votedFor = rf.totalServers + 1
		rf.lastReceived = time.Now()

		// For 2B
		rf.log = []LogEntry{}
		rf.commitIndex = 0 // To be initialized at 0 because log index starts from 1
		rf.lastApplied = 0

		firstEntry := LogEntry{Term: 0, Command: 0}
		rf.log = append(rf.log, firstEntry)
		return
	}
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	var currentTerm int
	var votedFor int
	var log []LogEntry
	if d.Decode(&currentTerm) != nil ||
		d.Decode(&votedFor) != nil ||
		d.Decode(&log) != nil {

	} else {
		////.Printf("[%v] Decoded data: currentTerm is : %v, votedFor : %v, log is : %v \n", rf.me, currentTerm, votedFor, log)
		rf.currentTerm = currentTerm
		rf.votedFor = votedFor
		rf.log = log
	}
}

//
// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
//
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
	Pf("###################### KILL CALLED ##############################")
	Pf("###################### KILL CALLED ##############################")
	Pf("###################### KILL CALLED ##############################")
	Pf("[%v] log len %v;; LOG : %v", rf.me, len(rf.log), rf.log)
	Pf("###################### KILL CALLED ##############################")
	Pf("###################### KILL CALLED ##############################")
	Pf("###################### KILL CALLED ##############################")
	Pf("###################### KILL CALLED ##############################")
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}
