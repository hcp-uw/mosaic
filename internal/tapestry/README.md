# Mosaic Tapestry

To manage the state of the cluster, nodes must share an identical set of events that tell the 
collective history of the events that changed said state. This story of state is Mosaic's Tapestry.

Each user will store a copy of the tapestry encoded as a protocol buffer data type.

***TBD NOTE > should the stun server have a copy of the tapestry?***


## Strands

The tapestry is made up of strands. A strand is a message sent at a specific time by one user 
that updates the shared state of all user. For example a strand can say a user became active
in the cluster or dropped connection. They will also be used to signal where data is stored in 
Mosaic.

### Header 
All strands relating to a user will consist of a header that identifies the user.

```
message Header {
    user_public_key
    date_time_stamp
    version_stamp
    
    signature: 
}

```                 

### UserOnline/UserOffline
The UserOnline message will note that a user connected to the network and is now able to receive
data/update the tapestry. The UserOffline message will show that a user has dropped connection.
***Note Leader logic will be based on the order in which the UserOnline message appears in the
tapestry***

```
message UserOnline {} //Header is automatically attached to the message identity is clear
message UserOffline {}
```


### DataSent/DataRecieved
These messages describe the process of storing a user's data. When a user wants to store data it 
will send a DataSent message. The first node that can store that data will add a DataRecieved 
confirmation message to the tapestry. Data retrieval will be used based on this DataRecieved 
reciept
```
message DataSent {
    // Header already denotes whos data it is 
    // encrypted file metadata will live here it will include what is below + TBD

    string shard_hash
    string filename
    string file_extension
    string file_size
}


message DataRecieved {
    // Header will denote who the data is stored with

    // User the data belongs to if not more we will see
    user_public_key
    
    // the shard hash will link this to the file metadata in the DataSent message
    string shard_hash
}
```

### DataMoved/DataDeleted
These messages will describe if the state of stored data changes in some way.
```
message DataMoved {
    // Header new datalocation
    
    // User the data belongs to if not more info we will see
    string user_public_key
    
    // the shard hash will link this to the file metadata in the DataSent message
    string shard_hash
}

message DataDeleted {
    // Header who deleted the data

    string user_public_key  // ties to the user and shard that was originally sent
    string shard_hash 
}

```





