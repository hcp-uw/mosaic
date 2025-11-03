from typing import *
from hashlib import sha1
from math import log2, floor
import random

def compute_hash(key: str) -> bytes:
    return sha1(key.encode()).digest()

def distance(id1: bytes | str, id2: bytes | str) -> int:
    if isinstance(id1, str):
        id1 = compute_hash(id1)
    if isinstance(id2, str):
        id2 = compute_hash(id2)
    return int.from_bytes(id1, 'big') ^ int.from_bytes(id2, 'big')

class Node:
    def __init__(self, id_: str):
        self.readable_id = id_
        self.id = compute_hash(id_)
        self.internal_data = {}
        
        self.k = 5 # k
        self.a = 3 # alpha

        self.buckets = [[] for _ in range(160)]
        
    @property
    def neighbors(self):
        return [node for bucket in self.buckets for node in bucket]

    def __repr__(self):
        return f"Node({self.readable_id})"
    
    def ping(self, node: 'Node') -> bool:
        return random.random() > 0.6
    
    def add_node(self, node: 'Node'):
        dist = distance(self.id, node.id)
        
        if dist == 0: return
        
        bucket_index = floor(log2(dist)) if dist > 0 else 0
        bucket = self.buckets[bucket_index]
        
        if node in bucket:
            bucket.remove(node)
            bucket.append(node)
        else:
            if len(bucket) < self.k:
                bucket.append(node)
            else:
                if self.ping(bucket[0]):
                    bucket.append(bucket.pop(0))
                    return
                else:
                    bucket.pop(0)
                    bucket.append(node)
                    
                    
    def closest_to(self, key, querier):
        
        self.add_node(querier)
        
        if isinstance(key, str):
            target_hash = compute_hash(key)
        else:
            target_hash = key
            
        # Collect all nodes from all buckets
        unique_nodes: Dict[bytes, 'Node'] = {}
        
        for bucket in self.buckets:
            for node in bucket:
                unique_nodes[node.id] = node

        final_list = list(unique_nodes.values())
        
        # Sort by distance to target
        final_list.sort(key=lambda node: distance(node.id, target_hash))
        
        return final_list[:self.k]
                    
    def store(self, dht_id, key: str, value: Any):
        if dht_id not in self.internal_data:
            self.internal_data[dht_id] = {}
        self.internal_data[dht_id][key] = value
        
    
class DHT(MutableMapping):
    def __init__(self, node: Node, identifier: str):
        self.node = node
        self.k = node.k
        self.a = node.a
        self.readable_id = identifier
        self.id = compute_hash(identifier)
        
        
    def discover(self, h):
        shortlist = self.node.closest_to(h)
        seen = {n.id for n in shortlist}
        seen.add(self.node.id)
        
        # Track nodes already queried to detect convergence
        queried = set()
        
        while True:
            # Get alpha closest nodes we haven't queried yet
            candidates = [n for n in shortlist if n.id not in queried][:self.a]
            
            if not candidates:
                break
            
            old_closest = {n.id for n in shortlist[:self.k]}
            
            new_results_from_batch = []
            
            for node in candidates:
                queried.add(node.id)
                
                for n2 in node.closest_to(h, self.node):
                    if n2.id not in seen:
                        new_results_from_batch.append(n2)
                        seen.add(n2.id)
            
            if not new_results_from_batch:
                continue 

            
            shortlist.extend(new_results_from_batch)
            shortlist = sorted(shortlist, key=lambda node: distance(h, node.id))
            
            new_closest = {n.id for n in shortlist[:self.k]}
            if new_closest == old_closest: break
            
        return shortlist[:self.k] 

        
    def __getitem__(self, key):
        h = compute_hash(key)
        shortlist = self.discover(h)
        assert len(shortlist) > 0

    
    def __setitem__(self, key, value):
        h = compute_hash(key)
        shortlist = self.discover(h)
        assert len(shortlist) > 0


