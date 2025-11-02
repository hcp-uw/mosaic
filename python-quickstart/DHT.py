from typing import *
from collections.abc import MutableMapping
import hashlib
import random


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
    K = 5
    def __init__(self, identifier):
        self.identifier = identifier

        self.internal_data: Dict[bytes, Dict[bytes, bytes]] = {} # data that the node itself contains
        self.internal_references: Dict[bytes, Dict[bytes, BaseNode]] = {} # "i know who has this data"

        self.buckets = [[] for _ in range(256)]

        # self.neighbors: Set[BaseNode] = set()

        self.hash = sha(identifier)

        self.data = DHT(self, "data")

    @property
    def neighbors(self):
        all_neighbors = set()
        for bucket in self.k_buckets:
            all_neighbors.update(bucket)
        return all_neighbors

    def bootstrap(self, bootstrap_node):
        self.neighbors.add(bootstrap_node) 
        
        # 2. Run discover(self.hash)
        #    This will query central, which *now* has the full list.
        nodes_found = self.data.discover(self.hash)
        
        # 3. We *replace* our old neighbors with the new, correct list.
        #    (We must keep 'self' and 'central')
        self.neighbors = {bootstrap_node, self}
        self.neighbors.update(nodes_found)


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
        candidates = list(self.neighbors) + [self]
        unique = {peer.hash: peer for peer in candidates}.values()
        sorted_peers = sorted(unique, key=lambda other: hash_distance(hashed, other.hash))

        if threshold == -1:
            return sorted_peers
        return sorted_peers[:threshold]
    
    def __repr__(self):
        return f"BaseNode({self.identifier}, connections={len(self.neighbors)})"

    
class Central(BaseNode):
    def __init__(self):
        super().__init__("central")
        # self.neighbors.add(self)
        self.all_nodes = {self.hash: self}

    def register(self, node: BaseNode):
        # closest = sorted([n for n in self.neighbors if n != node], key=lambda other: node_distance(node, other))
        # node.neighbors = set(closest[:3])

        # self.neighbors.add(node)
        ...

        node.bootstrap(self)
        self.all_nodes[node.hash] = node


    def closest_to(self, hashed, threshold=-1):
        candidates = list(self.all_nodes.values())
        
        unique = {peer.hash: peer for peer in candidates}.values()
        sorted_peers = sorted(unique, key=lambda other: hash_distance(hashed, other.hash))

        if threshold == -1:
            return sorted_peers
        return sorted_peers[:threshold]
        

class DHT(MutableMapping):
    def __init__(self, node, identifier, *args, **kwargs):
        self.node: BaseNode = node
        self.identifier = sha(identifier)

        self.config = {
            "use_references": True,
            "closest_threshold": -1,
            "query_threshold": 3,
            "use_references": True,
            "shortlist_threshold": 5
        }    

    def discover(self, hashed, single_return=False):
        initialcandidates = list(self.node.neighbors) + [self.node]
        unique = {peer.hash: peer for peer in initialcandidates}.values()

        seen = set()

        shortlist = sorted(unique, key=lambda peer: hash_distance(hashed, peer.hash))[:self.config["shortlist_threshold"]]
        staging = []
        while staging != shortlist:
            staging = shortlist
            query = []
            for peer in shortlist:

                if single_return:
                    potential = peer.get_data(self.identifier, hashed)
                    if potential:
                        return peer

                    referenced = peer.find_reference(self.identifier, hashed)
                    if referenced:
                        return referenced


                if peer.hash not in seen:
                    query.append(peer)
                if len(query) == self.config["query_threshold"]: break
        
            if not query: break

            new_peers = []
            for peer in query:
                new_peers.extend(peer.closest_to(hashed, threshold=self.config["closest_threshold"]))
                seen.add(peer.hash)

            combined = shortlist + new_peers
            unique = {peer.hash: peer for peer in combined}.values()

            shortlist = sorted(unique, key=lambda peer: hash_distance(hashed, peer.hash))[:self.config["shortlist_threshold"]]

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

for word in range(50):
    n = BaseNode(str(word))
    central.register(n)
    nodes[word] = n

for n in nodes:
    central.register(nodes)


nodes[10].data["key"] = "value"

for n in nodes:
    try:
        nodes[n].data["key"]
    except:
        pass
    else:
        print(n)