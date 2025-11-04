import unittest
from DHT import Central, BaseNode
import random

class DHT_Test(unittest.TestCase):
    def execute(self, N):
        central = Central()
        nodes = {}
        for word in range(N):
            n = BaseNode(str(word))
            central.register(n)
            nodes[word] = n

        for n in nodes:
            central.recalculate(nodes[n])

        nodes[random.randint(0, N-1)].data["test"] = "abcde"
        

        for node in nodes:
            self.assertEqual(nodes[node].data["test"], "abcde")


    def test_small_n(self):
        self.execute(5)

    def test_medium_n(self):
        self.execute(500)

    def test_large_n(self):
        self.execute(5000)

    

if __name__ == "__main__":
    unittest.main()