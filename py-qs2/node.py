from typing import *
from hashlib import sha1

def get_id(key: str) -> bytes:
    return sha1(key.encode()).digest()

class Node:
    def __init__(self, node_id: str, address: str, port: int):
        self.identifier = node_id
        self.id = get_id(node_id)
        self.address = address
        self.port = port
        
        self.k_buckets: Dict[int, List[Node]] = {i: [] for i in range(160)}  # Example for 160-bit IDs

class Kademlia:
    def __init__(self):
        pass
    
    def PING(self, node_id):
        pass
    
    def STORE(self, key, value):
        pass
    
    def FIND_NODE(self, target_id):
        pass
    
    def FIND_VALUE(self, key):
        pass
    
    