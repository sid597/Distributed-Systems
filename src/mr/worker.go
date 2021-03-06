package mr

import (
    "fmt"
    "log"
    "net/rpc"
    "hash/fnv"
    "os"
    "io/ioutil"
    "time"
    "encoding/json"
    "sort"
)



////////////////////////////////////////////////
// Declarations
////////////////////////////////////////////////

//
// Map functions return a slice of KeyValue.
//
type KeyValue struct {
	Key   string
	Value string
}


type WorkerDetails struct {
    Id int
    Pid int
    R int // No of reduce workers
    StartTime time.Time 
    State string  // state of worker : Map, Reduce, Done, Wait
    MapFileName string // Ts
    ReduceFileName string // TS
    ReduceFileNo int // TS
    Status string
    AllMapWorkers []int // TS
}

var CurrentWorker WorkerDetails


////////////////////////////////////////////////
// Main Functions  
////////////////////////////////////////////////

//
// main/mrworker.go calls this function.
//
func Worker(mapf func(string, string) []KeyValue, reducef func(string, []string) string) bool {
    CurrentWorker = GetTask()
    ///fmt.Println("Current worker details are :",CurrentWorker)
    // A special keyword to tell if all files are done
    for CurrentWorker.State != "Done" {
        if CurrentWorker.State == "Map"{
            MapTask(CurrentWorker.MapFileName, mapf)
             //time.Sleep(50 * time.Millisecond)   // Used to test map running in parallel or not
             TellMasterIAmDone(CurrentWorker)
            ///res := TellMasterIAmDone(CurrentWorker)
            ///fmt.Println("Master reply for our Done request, for Map worker ",CurrentWorker.Id,res.Message)

        } else if CurrentWorker.State == "Wait" {
            time.Sleep(1 * time.Second)
        } else if CurrentWorker.State == "Reduce" {
            ReduceTask(CurrentWorker.ReduceFileNo, CurrentWorker.AllMapWorkers, reducef)
            //time.Sleep(50 * time.Millisecond)   // Used to test reduce running in parallel or not
            TellMasterIAmDone(CurrentWorker)
            ///res := TellMasterIAmDone(CurrentWorker)
            ///fmt.Println("Master reply for our Done request, for Reduce  worker ",CurrentWorker.Id,res.Message)

        }

        //fmt.Println(CurrentWorker)
        CurrentWorker  = GetTask()
    }
    return true
}


////////////////////////////////////////////////
// Map Reduce functions 
////////////////////////////////////////////////

// for sorting by key.
type ByKey []KeyValue

// for sorting by key.
func (a ByKey) Len() int           { return len(a) }
func (a ByKey) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByKey) Less(i, j int) bool { return a[i].Key < a[j].Key }

// Perform map job for the current file with the passed map func
func MapTask(filename string, mapf func(string,string)[]KeyValue) bool {
        create := CreateInterFiles()
        if !create {
            return false
        }
		file, err := os.Open(filename)
		if err != nil {
			log.Fatalf("cannot open %v", filename)
            return false
		}
		content, err := ioutil.ReadAll(file)
		if err != nil {
			log.Fatalf("cannot read %v", filename)
            return false
		}
        //fmt.Println(content)
		file.Close()
        //fmt.Println(string(content))
		kva := mapf(filename, string(content))
        for _,kv := range kva {
            reduceFileNo := (ihash(kv.Key) % CurrentWorker.R)
            WriteMapTo(reduceFileNo,kv)
        }
        return true
}


// Perform Reduce Job for the current file with the passed reduce function
func ReduceTask(reduceFileNo int,listOfMapWorkerFiles []int, reducef func(string, []string)string ) bool {

    // get all the files to read from NXM buckets
    // load to memory(in a slice)
    intermediate := GetKvForReduce(listOfMapWorkerFiles, reduceFileNo)
    //fmt.Println("intermediate values are ",intermediate)
    // sort the slice, Map-Reduce paper states if the intermediate data is too large for memory
    // we send it for external sort
    sort.Sort(ByKey(intermediate))

    // create file to write to  
    reduceFile := fmt.Sprint("mr-out-", reduceFileNo)
    rFile, err := os.Create(reduceFile)
    if err != nil {
        return false
    }
    // reducef
	//
	// call Reduce on each distinct key in intermediate[],
	// and print the result to mr-out-*.
	//
    i := 0
	for i < len(intermediate) {
		j := i + 1
        // Find the first index which is different from intermediate[i].key
		for j < len(intermediate) && intermediate[j].Key == intermediate[i].Key {
			j++
		}
		values := []string{}
		for k := i; k < j; k++ {
			values = append(values, intermediate[k].Value)
		}
		output := reducef(intermediate[i].Key, values)
        //fmt.Println("intermediate key for reduce is : ",intermediate[i].Key)
        // write to file
		fmt.Fprintf(rFile, "%v %v\n", intermediate[i].Key, output)

		i = j
	}
    // close file
    rFile.Close()
    return true
}

////////////////////////////////////////////////
// Helper functions
////////////////////////////////////////////////
//
// use ihash(key) % NReduce to choose the reduce
// task number for each KeyValue emitted by Map.
//
func ihash(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}

// Tell Master worker is done
func TellMasterIAmDone(worker WorkerDetails ) MessageForWorker {
    Msg := MessageForWorker{}
    call("MasterDetails.WorkerDone", &worker, &Msg)
    ///fmt.Println("Message received is ",Msg.Message)
    return Msg
}

// Ask for new task from Master
func GetTask() WorkerDetails{
    na := NoArgs{}
    NewTask := WorkerDetails{}
    call("MasterDetails.AssignNewTask",&na, &NewTask)
    ///fmt.Println("Task is :", NewTask.Task)
    return NewTask
}


// Create R(no of reduce files to create) Temporary Map files 
func CreateInterFiles() bool  {
    //fmt.Println("creating new interm files",CurrentWorker.R)

    for i:= 0; i < CurrentWorker.R; i++{
        tFile := fmt.Sprint("mr-inter-" , CurrentWorker.Id , "-" , i ,".tmp")
        nFile,err := os.Create(tFile)
        if err != nil {
            fmt.Println(err)
            return false
        }
        nFile.Close()
    }
    return true
}

// Write Map KeyValue Slice output to R-th file
func WriteMapTo(reduceFileNo int,KV KeyValue) bool {

    filename := fmt.Sprint("mr-inter-", CurrentWorker.Id, "-", reduceFileNo, ".tmp")
    file, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, os.ModeAppend)
    if err != nil {
        fmt.Println("Error while writinf to map file", err)
    }
    enc := json.NewEncoder(file)
    er := enc.Encode(KV)
    if er != nil {
            fmt.Println(err)
    }
    file.Close()
    return true
}

// Read data from all the intermediate files for a specefic reduce task
func GetKvForReduce(listOfMapWorkerFiles []int, reduceFileNo int) []KeyValue {
    kva := []KeyValue{}
    for _, mapworker := range listOfMapWorkerFiles {
        interFile := fmt.Sprint("mr-inter-", mapworker, "-", reduceFileNo)
        file, err := os.Open(interFile)
        if err != nil {
            fmt.Println("err in GetKvForReduce", err)
        }
        dec := json.NewDecoder(file)
        for {
          var kv KeyValue
          if err := dec.Decode(&kv); err != nil {
            break
          }
          //fmt.Println("Kv for this ket is ",kv)
          kva = append(kva, kv)
        }
        file.Close()
    }
    return kva
}




////////////////////////////////////////////////
// Send RPC
////////////////////////////////////////////////
// send an RPC request to the master, wait for the response.
// usually returns true.
// returns false if something goes wrong.
//
func call(rpcname string, args interface{}, reply interface{}) bool {
	//, err := rpc.DialHTTP("tcp", "127.0.0.1"+":1534")
	sockname := masterSock()
	c, err := rpc.DialHTTP("unix", sockname)
	if err != nil {
        fmt.Println(err)
		log.Fatal("dialing:", err)
	}
	defer c.Close()

	err = c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}

    fmt.Println("Error in call function for server",err)
	return false
}
