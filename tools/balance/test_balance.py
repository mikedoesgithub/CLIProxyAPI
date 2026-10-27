"""Protocol/concurrency and supervised training tests, without downloading weights."""
import json
from pathlib import Path
import tempfile
import threading
import unittest
import urllib.error
import urllib.request

from classifier import HeadOnlyClassifier
from custom_head import Head
from server import create_server
from train_head import train


class ServiceTest(unittest.TestCase):
    def setUp(self):
        self.started = threading.Event()
        self.release = threading.Event()
        outer = self

        class Stub:
            def classify(self, request, context):
                if request == 'block':
                    outer.started.set()
                    if not outer.release.wait(3):
                        raise TimeoutError('test synchronization failed')
                return {'category': 'coding', 'category_confidence': .95, 'difficulty': 'hard', 'stakes': 'low'}

        self.server = create_server(Stub(), 0)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.base = f'http://127.0.0.1:{self.server.server_port}'

    def tearDown(self):
        self.release.set()
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(3)

    def call(self, data=None, path='/classify'):
        encoded = None if data is None else json.dumps(data).encode()
        request = urllib.request.Request(self.base + path, data=encoded, headers={'Content-Type': 'application/json'})
        try:
            with urllib.request.urlopen(request, timeout=3) as response:
                return response.status, json.load(response)
        except urllib.error.HTTPError as error:
            return error.code, json.load(error)

    def test_busy_requests_fail_fast_and_health_stays_responsive(self):
        responses = []
        request = threading.Thread(target=lambda: responses.append(self.call({'request': 'block'})))
        request.start()
        self.assertTrue(self.started.wait(3))
        self.assertEqual(self.call({'request': 'other'})[0], 503)
        self.assertEqual(self.call(path='/health')[0], 200)
        self.release.set()
        request.join(3)
        self.assertEqual(responses[0][0], 200)

    def test_protocol_limits(self):
        for data, status in [({'request': 'ok'}, 200), ({'request': 'a' * 4001}, 400),
                             ({'request': 'ok', 'context': 'a' * 601}, 400),
                             ({'request': 4}, 400), ({'request': 'a' * 70000}, 413), ([], 400)]:
            with self.subTest(data_type=type(data)):
                self.assertEqual(self.call(data)[0], status)
        self.assertEqual(self.call({'request': 'ok'}, '/wrong')[0], 404)


class TrainingTest(unittest.TestCase):
    def test_training_roundtrip_and_abstention_artifact(self):
        rows = []
        for i in range(24):
            rows += [{'text': f'Browser click navigate shopping cart product {i}', 'label': 'browser'},
                     {'text': f'Blender mesh material vertex render scene {i}', 'label': '3d'}]
        head, report = train(rows, epochs=20)
        self.assertEqual(report['train'] + report['validation'], 48)
        self.assertEqual(head.predict('Blender mesh material render')[0], '3d')
        self.assertEqual(head.predict('Browser click navigate shopping')[0], 'browser')
        with tempfile.TemporaryDirectory() as directory:
            path = str(Path(directory) / 'head.json')
            head.save(path, report)
            loaded = Head.load(path)
            self.assertEqual(head.predict('Blender mesh'), loaded.predict('Blender mesh'))

    def test_head_only_routes_task_and_difficulty_without_encoder(self):
        rows = []
        for i in range(24):
            rows += [{'text': f'Browser click navigate shopping cart product {i}', 'label': 'browser'},
                     {'text': f'Blender mesh complex topology geometry scene {i}', 'label': '3d'}]
        task, task_report = train(rows, epochs=20)
        difficulty_rows = [{'text': r['text'], 'label': 'easy' if r['label'] == 'browser' else 'hard'} for r in rows]
        difficulty, difficulty_report = train(difficulty_rows, epochs=20)
        with tempfile.TemporaryDirectory() as directory:
            task_path, difficulty_path = str(Path(directory) / 'tasks.json'), str(Path(directory) / 'difficulty.json')
            task.save(task_path, task_report)
            difficulty.save(difficulty_path, difficulty_report)
            classifier = HeadOnlyClassifier(task_path, difficulty_path)
            result = classifier.classify('Blender mesh complex topology geometry')
            self.assertEqual(result['category'], '3d')
            self.assertEqual(result['difficulty'], 'hard')
            self.assertFalse(result['abstain'])

    def test_duplicate_conflicts_and_insufficient_data_rejected(self):
        with self.assertRaises(ValueError):
            train([{'text': 'Same prompt', 'label': 'browser'}, {'text': 'same  prompt', 'label': '3d'}])
        with self.assertRaises(ValueError):
            train([{'text': 'Browser', 'label': 'browser'}])


if __name__ == '__main__':
    unittest.main()
