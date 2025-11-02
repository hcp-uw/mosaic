from typing import *
from collections.abc import MutableMapping
import hashlib


def sha(content):
    if isinstance(content, str):
        content = content.encode()
    elif not isinstance(content, bytes):
        raise ValueError("hash content must either be string or bytes-representable object")
    
    return hashlib.sha256(content).digest()

def htoi(hashed):
    return int.from_bytes(hashed)

def distance(h1, h2):
    return htoi(h1) ^ htoi(h2)

# base node class
class BaseNode:
    def __init__(self, identifier):
        self.identifier = identifier

        self.data: Dict[bytes, bytes] = {} # data that the node itself contains
        self.references: Dict[bytes, BaseNode] = {} # "i know who has this data"

        self.neighbors: Set[BaseNode] = set()

        self.hash = sha(identifier)

    def add(self, k, v):
        self.data[k] = v

    def reference(self, k, v):
        self.references[k] = v 
        # reference hash k with node v
        # is v a reference to the node itself or the node's identifier?
        # how would this work in go, or when we bring this to the actual network?
        # tbh idk (yet)

    def closest_to(self, hashed, threshold=-1):
        candidates = list(self.neighbors) + [self]
        unique = {peer.hash: peer for peer in candidates}.values()
        sorted_peers = sorted(unique, key=lambda other: distance(hashed, self.hash))

        if threshold == -1:
            return sorted_peers
        return sorted_peers[:threshold]
    

class DHT(MutableMapping):
    def __init__(self, node, identifier, *args, **kwargs):
        self.node: BaseNode = node
        self.identifier = identifier

        self.config = {
            "use_references": True
        }

    

    def discover(self, hashed, single_return=False):
        initialcandidates = list(self.node.neighbors) + [self]
        unique = {peer.hash: peer for peer in initialcandidates}.values()

        seen = set()

        shortlist = sorted(unique, key=lambda peer: distance(hashed, peer.hash))
        staging = []

        while staging != shortlist:
            staging = shortlist
            query = []
            for peer in shortlist:
                if peer.hash not in seen:
                    query.append(peer)
                if len(query) == 3: break
        
            if not query: break

            new_peers = []
            for peer in query:
                new_peers.extend(peer.closest_to(hashed))
                seen.add(peer.hash)

            combined = shortlist + new_peers
            unique = {peer.hash: peer for peer in combined}.values()

            shortlist = sorted(unique, key=lambda peer: distance(hashed, peer.hash))

        return shortlist



    def __setitem__(self, key, value):
        hashed = sha(key)
        shortlist = self.discover(hashed)
        

    def __getitem__(self, key):
        ...

    def __delitem__(self, key):
        ...

    def __iter__(self):
        ...
    
    def __len__(self):
        ...

    def _keytransform(self, key):
        ...