# Mosaic Cluster Leadership Design
## Lease-Based, Term-Fenced Server-Arbitrated Authority

---

## 1. Purpose

This document defines the leadership model used within a Mosaic cluster.

The objective of this design is to:

- Prevent split-brain scenarios
- Eliminate cluster fragmentation
- Provide deterministic failover
- Avoid the operational complexity of full distributed consensus
- Maintain compatibility with Mosaic’s CRDT-based replication model

Leadership in Mosaic is coordination-only. Data correctness is handled independently by CRDTs and repair mechanisms. The leader exists to coordinate cluster-level operations, not to guarantee data safety.

---

## 2. Design Principles

1. **Single Authority Source**  
   Leadership arbitration is centralized through a coordination server.

2. **Monotonic Authority**  
   Leadership transitions are governed by a strictly increasing term number.

3. **Time-Bounded Control**  
   Leadership is granted via a renewable lease with expiration.

4. **Fencing of Stale Leaders**  
   All cluster operations validate leadership term to prevent zombie authority.

5. **Crash and Partition Safety**  
   The system must converge automatically after node failures or network partitions.

---

## 3. System Components

### 3.1 Coordination Server

The coordination server is responsible only for leadership arbitration.  
It does not participate in replication, state machine execution, or data consensus.

Per cluster, the server maintains the following durable state:

- `cluster_id`
- `current_leader_id`
- `current_term` (monotonically increasing integer)
- `lease_expiration_timestamp`
- `lease_id` (unique random identifier)

This state **must be persisted durably**. Loss of term monotonicity compromises safety.

---

### 3.2 Cluster Nodes

Each node maintains:

- `highest_term_seen` (persisted to disk)
- `current_leader_id`
- Local lease validation logic

Nodes must persist `highest_term_seen` across restarts.

---

## 4. Term-Based Authority Model

### 4.1 Term Definition

A *term* is a strictly increasing integer representing a leadership epoch.

Properties:

- Terms are never reused.
- Terms never decrease.
- Each leadership assignment increments the term.

The term provides global ordering of leadership transitions.

---

### 4.2 Authority Rule

A leader is valid **only if**:

- Its term equals the cluster’s current term.
- Its lease has not expired.
- Its lease ID matches the server-issued lease.

Any message carrying a lower term is rejected.

---

## 5. Leadership Lifecycle

### 5.1 Initial Leadership

When a cluster is first formed:

1. A node requests leadership.
2. Server sets `current_term = 1`.
3. Server assigns the requesting node as leader.
4. Server issues:
   - `term`
   - `lease_expiration_timestamp`
   - `lease_id`

The node begins periodic lease renewal.

---

### 5.2 Lease Structure

Leadership is granted for a fixed duration.

Leader must periodically call:

```renew_lease(cluster_id, term, lease_id)```


If:
- Term matches server term
- Lease is still valid

Server extends expiration timestamp.

If renewal fails:
- Node must immediately step down.

---

### 5.3 Lease Expiration

If a leader:

- Crashes
- Disconnects
- Fails to renew lease

Then upon expiration:

- Server clears `current_leader_id`
- Leadership becomes available
- Term remains unchanged until reassignment

---

### 5.4 Re-Election Process

When nodes detect leader failure:

1. Any node may request leadership.
2. Server checks:
   - If no active lease exists:
     - Increment `current_term`
     - Assign new leader
     - Issue new lease
   - If active lease exists:
     - Reject request

Only one node receives the new term and lease.

---

## 6. Cluster Messaging Requirements

All coordination messages must include:

- `leader_term`
- `lease_id`

Nodes enforce:

```if message.term < highest_term_seen:
reject message```


If a node receives a message with a higher term:

- Update `highest_term_seen`
- Step down if currently leader
- Accept new authority

This fencing mechanism eliminates zombie leaders and split-brain.

---

## 7. Failure Scenarios

### 7.1 Leader Crash

- Lease expires.
- New node requests leadership.
- Server increments term.
- Cluster resumes under new authority.

---

### 7.2 Network Partition

If partition occurs:

- One side may continue seeing old leader.
- Other side may request new leadership.

If lease expires:
- Server increments term.
- Issues new leadership.

When partition heals:
- Nodes compare terms.
- Lower term leaders step down.
- Cluster converges automatically.

---

### 7.3 Entire Cluster Failure

If all nodes go offline:

- Lease expires.
- Server retains `current_term`.
- Cluster becomes dormant.

When a node reconnects:

- It requests leadership.
- Server increments term.
- Issues new lease.
- Cluster resumes safely.

---

### 7.4 Server Restart

Server must persist:

- `current_term`
- `current_leader_id`
- `lease_expiration_timestamp`

If server loses term state, term monotonicity breaks and split-brain risk is introduced.

Durable persistence is mandatory.

---

## 8. Safety Guarantees

This design guarantees:

- At most one valid leader per term
- Automatic invalidation of stale leaders
- Deterministic failover
- Convergence after partitions
- No quorum or majority voting required
- No distributed log replication complexity

---

## 9. Scope and Limitations

This model provides coordination safety, not data consensus.

It assumes:

- The coordination server is reachable for arbitration.
- Server state is durable.
- Nodes enforce term fencing strictly.

This design intentionally avoids full distributed consensus mechanisms such as Raft. It is optimized for Mosaic’s CRDT-based data model and NAT-heavy P2P deployment environment.

---

## 10. Summary

Mosaic uses a lease-based, term-fenced leadership model where:

- The coordination server arbitrates authority.
- Leadership is time-bound.
- Authority is versioned via monotonically increasing terms.
- Stale leaders are automatically fenced.

This approach provides stable coordination with minimal complexity while preventing fragmentation and split-brain within the cluster.

