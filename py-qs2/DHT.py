import hashlib
import random
from typing import *

hash: Callable[[str], bytes] = lambda x: hashlib.sha1(x.encode()).digest()

def distance(a: bytes | str, b: bytes | str):
    if isinstance(a, str):
        a = hash(a)
    if isinstance(b, str):
        b = hash(b)
    return int.from_bytes(a) ^ int.from_bytes(b)

class Node:
    def __init__(self, node_id: str):
        self.node_id = node_id
        self.id = hash(node_id)
        self.data = {}

        self.k = 5

        self.buckets = [[] for _ in range(160)]  

    @property
    def neighbors(self):
        return [node for bucket in self.buckets for node in bucket]

    def __repr__(self):
        return f"Node({self.node_id})"
    
    def store(self, dht_id: str, key: str, value: Any):
        if dht_id not in self.data:
            self.data[dht_id] = {}
        self.data[dht_id][key] = value

    def retrieve(self, dht_id: str, key: str) -> Any:
        if dht_id in self.data and key in self.data[dht_id]:
            return self.data[dht_id][key]
        return None
    
    def ping(self):
        return random.random() > 0.4
    
    def add_node(self, node: 'Node'):
        if node == self or node.id == self.id:  # Don't add yourself
            return

        dist = distance(self.id, node.id)

        if dist == 0: return # double check

        bucket_index = dist.bit_length() - 1
        b = self.buckets[bucket_index]
        if node not in b:
            if len(b) < self.k:
                b.append(node)
            else:
                if b[0].ping():
                    temp = b.pop(0)
                    b.append(temp)
                else:
                    b.pop(0)
                    b.append(node)
        else:
            b.remove(node)
            b.append(node)

    def closest_to(self, target: bytes, querier: 'Node'):
        self.add_node(querier)  # Ensure the querier is added to routing table
        difference = distance(self.id, target)
        if difference == 0: 
            return [self]
        
        bucket_index = difference.bit_length() - 1
        candidates = []
        candidates.extend(self.buckets[bucket_index])
        
        lower = bucket_index - 1
        upper = bucket_index + 1
        while (lower >= 0 or upper < len(self.buckets)) and len(candidates) < self.k:
            if upper < len(self.buckets):
                candidates.extend(self.buckets[upper])
                upper += 1
            if lower >= 0:
                candidates.extend(self.buckets[lower])
                lower -= 1

        candidates.sort(key=lambda n: distance(n.id, target))
        return candidates[:self.k]
    
class DHT(MutableMapping):
    def __init__(self, node, data_id):
        self.node = node
        self.data_id = data_id

    def discover(self, h: bytes):
        closest = self.node.closest_to(h, self.node)  # Start with local k closest
        queried = set()
        queried.add(self.node.id)

        while True:
            # Find nodes to query (closest nodes not yet queried)
            to_query = [n for n in closest if n.id not in queried]
            if not to_query:
                break  # All closest nodes have been queried
            
            for peer in to_query[:self.node.k]:  # Query up to k nodes
                self.node.add_node(peer)
                queried.add(peer.id)

                new_nodes = peer.closest_to(h, self.node)
                for n in new_nodes:
                    self.node.add_node(n)
                    if n.id not in queried and n not in closest:
                        closest.append(n)
                    
            # Keep only k closest nodes
            closest = sorted(closest, key=lambda n: distance(n.id, h))[:self.node.k]
        
        return closest


    def __setitem__(self, key: str, value: Any):
        h = hash(key)
        shortlist = self.discover(h)

        for node in shortlist:
            node.store(self.data_id, key, value)

    def __getitem__(self, key: str):
        h = hash(key)
        shortlist = self.discover(h)
        if not shortlist:
            raise KeyError(f"Key {key} not found in {self}")

        for node in shortlist:
            result = node.retrieve(self.data_id, key)
            if result is not None:
                return result

        raise KeyError(f"Key {key} not found in {self}")

    def __iter__(self):
        raise NotImplementedError()
    
    def __delitem__(self, key: str):
        raise NotImplementedError()
    
    def __len__(self):
        raise NotImplementedError()

    def __repr__(self):
        return f"DHT({self.data_id})"

# random.seed(95)

nodes = {}
nodes[0] = Node("0")

for i in range(1, 101):
    id_ = str(i)
    n = Node(id_)
    nodes[i] = n

    nodes[0].add_node(n)
    n.add_node(nodes[0])

for _ in range(1):
    for i in range(0, 101):
        n = nodes[i]
        DHT(n, "temporary").discover(hash(str(random.random())))



for _ in range(100):
    n = nodes[(random.randint(0, 100))]
    dht = DHT(n, "mydata")
    key = f"key{random.randint(0,1000)}"
    v = f"value{random.randint(0,1000)}"
    dht[key] = v

    for node in nodes.values():
        dht_node = DHT(node, "mydata")
        assert dht_node[key] == dht[key]