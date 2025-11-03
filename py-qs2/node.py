from typing import *
from hashlib import sha1
from math import log2, floor

def get_hash(key: str) -> bytes:
    return sha1(key.encode()).digest()

def distance(id1: bytes | str, id2: bytes | str) -> int:
    if isinstance(id1, str):
        id1 = get_hash(id1)
    if isinstance(id2, str):
        id2 = get_hash(id2)
    return int.from_bytes(id1, 'big') ^ int.from_bytes(id2, 'big')

class Node:
    def __init__(self, id_: str):
        self.readable_id = id_
        self.id = get_hash(id_)
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
        return True # TODO: replace somehow
    
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
                    return
                else:
                    bucket.pop(0)
                    bucket.append(node)
                    
                    
    def closest_to(self, key):
        target_hash = get_hash(key)
        target_dist = distance(self.id, target_hash)
    
        target_bucket_index = floor(log2(target_dist)) if target_dist > 0 else 0
        
        unique_nodes: Dict[bytes, 'Node'] = {}
        
        for node in self.buckets[target_bucket_index]:
            unique_nodes[node.id] = node
                
        for i in range(1, 160):
            
            closer_index = target_bucket_index - i
            if closer_index >= 0:
                for node in self.buckets[closer_index]:
                    unique_nodes[node.id] = node

            farther_index = target_bucket_index + i
            if farther_index < 160:
                for node in self.buckets[farther_index]:
                    unique_nodes[node.id] = node
            
            if len(unique_nodes) >= self.k and (closer_index < 0 and farther_index >= 160):
                break

        final_list = list(unique_nodes.values())
        
        final_list.sort(key=lambda node: distance(node.id, target_hash))
        
        return final_list[:self.k]
                    
    def store(self, dht_id, key: str, value: Any):
        if dht_id not in self.internal_data:
            self.internal_data[dht_id] = {}
        self.internal_data[dht_id][key] = value
        
    
class DHT(MutableMapping):
    def __init__(self, node: Node):
        self.node = node
        self.k = node.k
        
        
    def __getitem__(self, key):
        raise NotImplementedError()
    
    def __setitem__(self, key, value):
        raise NotImplementedError()
        
        
    