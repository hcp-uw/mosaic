import unittest
from DHT import Node, DHT, hash
import random

class DHT_Test(unittest.TestCase):
    def execute(self, N):
        nodes = {}
        nodes[0] = Node("0")

        for i in range(1, N + 1):
            id_ = str(i)
            n = Node(id_)
            nodes[i] = n

            nodes[0].add_node(n)
            n.add_node(nodes[0])

        for _ in range(1):
            for i in range(0, N + 1):
                n = nodes[i]
                DHT(n, "temporary").discover(hash(str(random.random())))



        for _ in range(N):
            n = nodes[(random.randint(0, N))]
            dht = DHT(n, "mydata")
            key = f"key{random.randint(0,1000)}"
            v = f"value{random.randint(0,1000)}"
            dht[key] = v

            for node in nodes.values():
                dht_node = DHT(node, "mydata")
                assert dht_node[key] == dht[key]

    def test_small_n(self):
        self.execute(5)

    def test_medium_n(self):
        self.execute(500)

    def test_large_n(self):
        self.execute(5000)

    

if __name__ == "__main__":
    unittest.main()