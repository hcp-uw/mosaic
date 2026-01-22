# Mosaic Tapestry

To manage the state of the cluster, nodes must share an identical set of events that tell the 
collective history of the events that changed said state. This story of state is Mosaic's Tapestry.

Each user will store a copy of the tapestry encoded as a protocol buffer data type.

***TBD NOTE > should the stun server have a copy of the tapestry?***


### Strands

The tapestry is made up of strands. A strand is a message sent at a specific time by one user 
that updates the shared state of all user. For example a strand can say a user became active
in the cluster or dropped connection. They will also be used to signal where data is stored in 
Mosaic.

#### Header 
All strands relating to a user will consist of a header that identifies the user.

```
message Header {
    user_public_key:
    date_time_stamp: 
    version_stamp: 
    
    signature: 
}

```

#### UserOnline/UserOffline
The UserOnline message will note that a user connected to the network and is now able to receive
data/update the tapestry. The UserOffline message will show that a user has dropped connection.

```
message UserOnline {} //Header is automatically attached to the message so we know who it is
message UserOffline {}
```


