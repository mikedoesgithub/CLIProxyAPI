"""Small retrainable lexical task head. This does not fine-tune the s1 encoder."""
import hashlib
import json
import re

import numpy as np

DIMENSIONS = 8192
VERSION = 'blake2b-word-bigram-v1'


def features(text):
    words = re.findall(r'\w+', text.lower()[-4000:])
    terms = words + [a + ' ' + b for a, b in zip(words, words[1:])]
    counts = {}
    for term in terms:
        index = int.from_bytes(hashlib.blake2b(term.encode(), digest_size=8).digest(), 'little') % DIMENSIONS
        counts[index] = counts.get(index, 0) + 1
    indices = np.array(sorted(counts), dtype=np.int64)
    values = np.array([1 + np.log(counts[i]) for i in indices], dtype=np.float32)
    norm = np.linalg.norm(values)
    if norm:
        values /= norm
    return indices, values


class Head:
    def __init__(self, labels, weights, bias, temperature=1.0, threshold=0.8):
        self.labels, self.weights, self.bias = labels, weights, bias
        self.temperature, self.threshold = temperature, threshold

    def probabilities(self, text):
        indices, values = features(text)
        logits = (self.weights[:, indices] @ values + self.bias) / self.temperature
        exp = np.exp(logits - logits.max())
        return exp / exp.sum()

    def predict(self, text):
        p = self.probabilities(text)
        return self.labels[int(p.argmax())], float(p.max())

    @classmethod
    def load(cls, path):
        from pathlib import Path
        data = json.loads(Path(path).read_text())
        if data['version'] != VERSION or data['dimensions'] != DIMENSIONS:
            raise ValueError('incompatible custom head')
        labels = data['labels']
        weights, bias = np.asarray(data['weights'], dtype=np.float32), np.asarray(data['bias'], dtype=np.float32)
        temperature, threshold = float(data['temperature']), float(data['threshold'])
        if not 2 <= len(labels) <= 64 or len(set(labels)) != len(labels) or any(not isinstance(x, str) or not re.fullmatch(r'[a-z0-9_\-]{1,64}', x) for x in labels):
            raise ValueError('invalid task labels')
        if weights.shape != (len(labels), DIMENSIONS) or bias.shape != (len(labels),) or not np.isfinite(weights).all() or not np.isfinite(bias).all() or not 0.1 <= temperature <= 10 or not 0.5 <= threshold <= 1:
            raise ValueError('invalid custom head parameters')
        return cls(labels, weights, bias, temperature, threshold)

    def save(self, path, report):
        from pathlib import Path
        Path(path).write_text(json.dumps({'version': VERSION, 'dimensions': DIMENSIONS, 'labels': self.labels,
                                         'weights': self.weights.tolist(), 'bias': self.bias.tolist(),
                                         'temperature': self.temperature, 'threshold': self.threshold,
                                         'evaluation': report}, allow_nan=False))
