from typing import *
from collections.abc import MutableMapping
import hashlib
import random
import heapq


def sha(content):
    if isinstance(content, str):
        content = content.encode()
    elif not isinstance(content, bytes):
        raise ValueError("hash content must either be string or bytes-representable object")
    
    return hashlib.sha256(content).digest()

def htoi(hashed):
    return int.from_bytes(hashed)

def hash_distance(h1, h2):
    return htoi(h1) ^ htoi(h2)

def node_distance(n1, n2):
    # return random.random()
    return hash_distance(n1.hash, n2.hash)

class NetworkError(Exception):
    pass

# base node class
class BaseNode:
    def __init__(self, identifier):
        self.identifier = identifier

        self.internal_data: Dict[bytes, Dict[bytes, bytes]] = {} # data that the node itself contains
        self.internal_references: Dict[bytes, Dict[bytes, BaseNode]] = {} # "i know who has this data"

        self.buckets = [[] for _ in range(256)]

        # self.neighbors: Set[BaseNode] = set()

        self.hash = sha(identifier)

        self.data = DHT(self, "data")

        self.k = self.data.config["shortlist_threshold"] + 1

        self._neighbors_dirty = None
        self._neighbors_cache = None

    def _get_bucket_index(self, target_hash):
        distance = hash_distance(self.hash, target_hash)
        if distance == 0:
            return None  
        return distance.bit_length() - 1

    def add_contact(self, node):
        bucket_idx = self._get_bucket_index(node.hash)
        if bucket_idx is None:
            return
        
        bucket = self.buckets[bucket_idx]
        
        if node in bucket:
            # Already have it - move to end (most recently seen)
            bucket.remove(node)
            bucket.append(node)
        elif len(bucket) < self.k:
            # Bucket not full - just add it
            bucket.append(node)
        else:
            # Bucket full - EVICT OLDEST, add new node
            bucket.pop(0)  # remove least recently seen
            bucket.append(node)
        self._neighbors_dirty = True

    @property
    def neighbors(self):
        if self._neighbors_dirty:
            self._neighbors_cache = set()
            for bucket in self.buckets:
                self._neighbors_cache.update(bucket)
            self._neighbors_dirty = False
        return self._neighbors_cache

    def bootstrap(self, bootstrap_node):
        
        # Add bootstrap node to our buckets
        self.add_contact(bootstrap_node)
        
        # Find nodes close to us
        nodes_found = bootstrap_node.closest_to(self.hash, threshold=self.k)
        
        # Add all discovered nodes to our k-buckets
        for node in nodes_found:
            if node != self:
                self.add_contact(node)


    def add(self, identifier, k, v):

        if identifier not in self.internal_data:
            self.internal_data[identifier] = {}

        self.internal_data[identifier][k] = v

    def reference(self, identifier, k, v):
        if identifier not in self.internal_references:
            self.internal_references[identifier] = {}
        self.internal_references[identifier][k] = v 
        # reference hash k with node v
        # is v a reference to the node itself or the node's identifier?
        # how would this work in go, or when we bring this to the actual network?
        # tbh idk (yet)

    def find_reference(self, identifier, k):
        if identifier not in self.internal_references:
            return None
        return self.internal_references[identifier].get(k)
    
    def get_data(self, identifier, k):
        if identifier not in self.internal_data:
            return None
        return self.internal_data[identifier].get(k)

    def closest_to(self, hashed, threshold=-1):
        """Find closest nodes using k-buckets efficiently"""
        bucket_idx = self._get_bucket_index(hashed)
        
        candidates = []
        
        if bucket_idx is not None:
            # Start with the target bucket
            candidates.extend(self.buckets[bucket_idx])
            
            # Expand outward to neighboring buckets until we have enough
            distance = 1
            while len(candidates) < (threshold if threshold > 0 else 50):
                if bucket_idx - distance >= 0:
                    candidates.extend(self.buckets[bucket_idx - distance])
                if bucket_idx + distance < 256:
                    candidates.extend(self.buckets[bucket_idx + distance])
                distance += 1
                if bucket_idx - distance < 0 and bucket_idx + distance >= 256:
                    break
        
        # Add ourselves as a candidate
        candidates.append(self)
        
        # Remove duplicates
        unique = {peer.hash: peer for peer in candidates}
        
        key_func = lambda other: hash_distance(hashed, other.hash)

        if threshold == -1:
            return sorted(unique.values(), key=key_func)

        return heapq.nsmallest(threshold, unique.values(), key=key_func)
        
    def __repr__(self):
        return f"BaseNode({self.identifier}, connections={len(self.neighbors)})"

    
class Central(BaseNode):
    def __init__(self):
        super().__init__("central")
        self.all_nodes = {self.hash: self}

    def register(self, node: BaseNode):
        node.bootstrap(self)
        self.all_nodes[node.hash] = node
        # Don't need add_contact - Central uses all_nodes directly

    def add_contact(self, node):
        pass

    @property
    def neighbors(self):
        return set(self.all_nodes.values()) - {self}

    def closest_to(self, hashed, threshold=-1):
        # Central is omniscient - use all_nodes, not buckets
        candidates = list(self.all_nodes.values())
        
        key_func = lambda other: hash_distance(hashed, other.hash)

        if threshold == -1:
            return sorted(candidates, key=key_func)
        
        return heapq.nsmallest(threshold, candidates, key=key_func)
        

class DHT(MutableMapping):
    def __init__(self, node, identifier, *args, **kwargs):
        self.node: BaseNode = node
        self.identifier = sha(identifier)

        self.config = {
            "use_references": True,
            "closest_threshold": 5,
            "query_threshold": 3,
            "shortlist_threshold": 5
        }    

    def discover(self, hashed, single_return=False):
        initialcandidates = self.node.neighbors | {self.node}

        seen = set()
        
        key_func = lambda peer: hash_distance(hashed, peer.hash)
        k = self.config["shortlist_threshold"]

        shortlist = heapq.nsmallest(k, initialcandidates, key=key_func)
        staging = []
        while staging != shortlist:
            staging = shortlist
            query = []
            for peer in shortlist:

                

                if peer.hash not in seen:
                    query.append(peer)
                if len(query) == self.config["query_threshold"]: break
        
            if not query: break

            new_peers = []
            for peer in query:
                self.node.add_contact(peer)

                if single_return:
                    potential = peer.get_data(self.identifier, hashed)
                    if potential:
                        return peer

                    referenced = peer.find_reference(self.identifier, hashed)
                    if referenced:
                        return referenced


                adjacent = peer.closest_to(hashed, threshold=self.node.k)

                for p in adjacent:
                    if p.hash not in seen:
                        self.node.add_contact(p)

                new_peers.extend(adjacent)
                seen.add(peer.hash)

            combined = {peer.hash: peer for peer in shortlist}
            for peer in new_peers:
                combined[peer.hash] = peer

            shortlist = heapq.nsmallest(k, combined.values(), key=key_func)

        if single_return:
            return None
        return shortlist



    def __setitem__(self, key, value):
        hashed = sha(key)
        shortlist = self.discover(hashed)
        

        if len(shortlist) == 0:
            raise NetworkError("unable to find peers to set {" + repr({key: value}) + "}")

        if self.config["use_references"]:
            storage = shortlist[0]
            storage.add(self.identifier, hashed, value)
            for node in shortlist[1:]:
                node.reference(self.identifier, hashed, storage)
        else:
            for node in shortlist:
                node.add(self.identifier, hashed, value)

    def __getitem__(self, key):
        hashed = sha(key)
        peer = self.discover(hashed, single_return=True)
        if not peer: 
            raise NetworkError(f"key \"{key}\" not found in {self}") 
        
        return peer.get_data(self.identifier, hashed)
        
    def __repr__(self):
        return f"DHT({self.identifier})"

    def __delitem__(self, key):
        ...

    def __iter__(self):
        ...
    
    def __len__(self):
        ...


central = Central()

nodes = {}

import time

start = time.time()
a = time.time()
N = 5

for word in range(N):
    n = BaseNode(str(word))
    central.register(n)
    nodes[word] = n

# After registration, before lookups
neighbor_counts = [len(nodes[i].neighbors) for i in range(N)]
print(f"Neighbors: min={min(neighbor_counts)}, max={max(neighbor_counts)}, avg={sum(neighbor_counts)/len(neighbor_counts):.1f}")

# After registering all nodes
print(f"Central has {len(central.neighbors)} total neighbors")
print(f"Central.all_nodes has {len(central.all_nodes)} entries")

# Should be equal!
if len(central.neighbors) != len(central.all_nodes):
    print("BUG: Central's buckets don't match all_nodes!")
    
# Check bucket distribution
filled_buckets = sum(1 for b in central.buckets if b)
print(f"Central has {filled_buckets} non-empty buckets")
# for i, bucket in enumerate(central.buckets):
#     if len(bucket) > 0:
#         print(f"  Bucket {i}: {len(bucket)} nodes (max={central.k})")


print(f"Register {N} nodes: {(time.time() - a):.4f}s")

# for n in nodes:
#     central.register(nodes[n])

a = time.time()
nodes[0].data["key"] = "value"
print(f"Set single K/V: {(time.time() - a):.4f}s")

a = time.time()
error = 0
for n in nodes:
    try:
        assert nodes[n].data["key"] == "value"
    except:
        # print(f"Error on node {n}")
        error += 1
    else:
        ...

print(f"{error} lookup errors")

print(f"Retrieve on {N} nodes: {(time.time() - a):.4f}s")

print(f"Total: {time.time() - start:.4f}s")