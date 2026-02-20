
# Leader Transfer Determination

This document outlines the process of leader selection, detection of leader loss, and reassignment within a node cluster. It includes key terms, node responsibilities, and potential mechanisms to ensure a single leader at all times.

---

## Key Terms

- **Leader** – The node responsible for coordinating a cluster.  
- **Cluster** – A group of nodes working together under a single leader.  
- **Server** – Central entity that initially assigns the leader and tracks cluster membership.  
- **Ping Routine** – Periodic communication between nodes and the leader to verify connectivity.  
- **STUN Server** – Service used to coordinate leader selection and node discovery.  
- **LeaderLost** – Notification message sent by nodes when they detect the leader is no longer reachable.  
- **Leader Queue** – Mechanism to prevent multiple nodes from simultaneously attempting to become the leader.

---

## Option A: Leader Selection and Transfer

### 1. Initial Leader Selection
1. The **server** assigns the **first leader** as the first node to connect.  
2. The leader node is notified of its role.  
3. The server **does not interrupt the leader’s ping routine**.  
4. Any subsequent nodes attempting to join the cluster are:
   - Temporarily connected to the current leader.  
   - Immediately dropped from the server’s direct connection.  
5. **Responsibility** for onboarding or coordinating new nodes now resides with the leader.

---

### 2. Handling Leader Disconnection
1. When the **leader disconnects**, the server:
   - Does **not take any action** to reassign leadership.  
   - Assumes the **leader slot is empty**.  
   - Awaits the next node to join, which may become the new leader.  
2. Cluster nodes detect the leader loss by observing that their **ping routine fails**.  
3. Nodes respond by sending a **`LeaderLost`** message to the STUN server.

---

### 3. Leader Reassignment
1. **No existing leader**:  
   - The first node to send a `LeaderLost` message **becomes the new leader**.  
2. **New leader already assigned**:  
   - The STUN server directs all cluster nodes to **connect to the new leader**.  
3. **Leader Queue Mechanism (TBD)**:
   - To prevent multiple nodes from attempting leadership simultaneously, a **leader queue** can be used.  
   - Each node in the cluster is assigned an **incrementing queue number** by the server.  
   - Only the node at the front of the queue is eligible to become leader.

---

## Notes / TBD
- Determine the precise mechanism for **queue assignment** and **leader election tie-breaking**.  
- Decide if leader queue numbers persist across node reconnects.  
- Consider heartbeat interval adjustments to ensure quick detection of leader loss.  
- Define whether nodes can preemptively prepare for leadership to minimize downtime.  

