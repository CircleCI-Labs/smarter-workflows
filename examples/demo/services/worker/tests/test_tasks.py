import unittest

from worker import tasks


class NormalizeNameTest(unittest.TestCase):
    def test_trims_blanks(self):
        self.assertEqual(tasks.normalize_name("  Ada "), "Ada")

    def test_blank_is_the_world(self):
        self.assertEqual(tasks.normalize_name("   "), "world")
        self.assertEqual(tasks.normalize_name(None), "world")


class GreetingsTest(unittest.TestCase):
    def test_one_greeting_per_name(self):
        got = tasks.greetings(["Ada", " ", "Grace"])
        self.assertEqual([g["name"] for g in got], ["Ada", "world", "Grace"])
        self.assertEqual(got[0]["message"], "Hello, Ada!")

    def test_empty_list(self):
        self.assertEqual(tasks.greetings([]), [])


if __name__ == "__main__":
    unittest.main()
