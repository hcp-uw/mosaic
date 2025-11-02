import random
from hashlib import sha256

def log(string):
    return
    print(string)

class Peer:
    def __init__(self, id_):
        self.neighbors = set()
        self.id = id_

        self.points = {
            # (k, v) indicates
            # "the item with hash k definitely is stored in v"
        }

        self.data = {
            # (k, v) indicates
            # "v is the data stored with hash k
        }

        self.hash = int('0x' + sha256(self.id.encode()).hexdigest(), 16)

    def closest_to(self, sha_hash):
        candidates = list(self.neighbors) + [self]
        unique = {peer.hash: peer for peer in candidates}.values()
        sorted_peers = sorted(unique, key=lambda other: (sha_hash ^ other.hash))
        return sorted_peers[:5]
    
    def recalculate_neighbors(self, central):
        central.register(self)

    def dispatch(self, data):
        sha_hash = int('0x' + sha256(data.encode()).hexdigest(), 16)
        shortlist = self.discover(sha_hash)
        shortlist = [peer for peer in shortlist if peer != self]

        if len(shortlist) == 0: 
            return None

        storage = shortlist[0]
        storage.data[sha_hash] = data

        for indexer in shortlist:
            if indexer != storage:
                indexer.points[sha_hash] = storage

        return sha_hash


    def discover(self, sha_hash):
        
        seen = set()

        initial_candidates = list(self.neighbors) + [self] # Add self
        unique = {peer.hash: peer for peer in initial_candidates}.values()
        shortlist = sorted(unique, key=lambda x: (sha_hash ^ x.hash))[:5]
        last_shortlist = []

        hops = 0

        while last_shortlist != shortlist:
            last_shortlist = shortlist
            query = []
            for peer in shortlist:
                if peer.hash not in seen:
                    query.append(peer)
                if len(query) == 3: break
        
            if not query: break

            new_peers = []
            for peer in query:
                new_peers.extend(peer.closest_to(sha_hash))
                seen.add(peer.hash)

            combined = shortlist + new_peers
            unique = {peer.hash: peer for peer in combined}.values()
            shortlist = sorted(unique, key=lambda x: (sha_hash ^ x.hash))[:5]
            hops += 1

        return shortlist
    
    def retrieve(self, sha_hash):
        seen = set()

        initial_candidates = list(self.neighbors) + [self] # Add self
        unique = {peer.hash: peer for peer in initial_candidates}.values()
        shortlist = sorted(unique, key=lambda x: (sha_hash ^ x.hash))[:5]
        last_shortlist = []

        while last_shortlist != shortlist:
            last_shortlist = shortlist
            query = []
            for peer in shortlist:

                if sha_hash in peer.data:
                    return peer.data[sha_hash]

                if sha_hash in peer.points:
                    return peer.points[sha_hash].data[sha_hash]

                if peer.hash not in seen:
                    query.append(peer)
                if len(query) == 3: break
        
            if not query: break

            new_peers = []
            for peer in query:
                new_peers.extend(peer.closest_to(sha_hash))
                seen.add(peer.hash)

            combined = shortlist + new_peers
            unique = {peer.hash: peer for peer in combined}.values()
            shortlist = sorted(unique, key=lambda x: (sha_hash ^ x.hash))[:5]

        return None

    def __repr__(self):
        return f"Node({self.id})"


def peer_distance(a: Peer, b: Peer):
    return random.random() # to be replaced later. this is just for demo purposes

class Central(Peer):
    def __init__(self):
        super().__init__("central")
        self.neighbors.add(self)

    def register(self, node: Peer):
        closest = sorted(self.neighbors, key=lambda other: peer_distance(node, other))
        node.neighbors = set(closest[:3])

        log(f"{node} registered with neighbors {node.neighbors}")

        self.neighbors.add(node)


central = Central()

nodes = {}

for word in range(50):
    n = Peer(str(word))
    central.register(n)
    nodes[word] = n


log(f"{len(nodes)} nodes initialized")

a = nodes[0]
a.recalculate_neighbors(central)
h = a.dispatch("hello world")

print(a.retrieve(h))