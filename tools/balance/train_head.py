#!/usr/bin/env python3
"""Train a tiny task head from private JSONL {"text": ..., "label": ...} examples."""
import argparse
import collections
import json
import re

import numpy as np

from custom_head import DIMENSIONS, Head, features


def train(rows, epochs=80, seed=42):
    unique = {}
    for row in rows:
        text, label = row['text'], row['label']
        if not isinstance(text, str) or not text.strip() or not isinstance(label, str) or not re.fullmatch(r'[a-z0-9_\-]{1,64}', label):
            raise ValueError('each row needs text and a lowercase task label')
        key = ' '.join(text.lower().split())
        if key in unique and unique[key][1] != label:
            raise ValueError('duplicate text with conflicting labels')
        unique[key] = (text, label)
    groups = collections.defaultdict(list)
    for text, label in unique.values():
        groups[label].append(text)
    if not 2 <= len(groups) <= 64 or min(map(len, groups.values())) < 20:
        raise ValueError('need at least two labels and 20 distinct examples per label')
    labels = sorted(groups)
    rng = np.random.default_rng(seed)
    training, validation = [], []
    for y, label in enumerate(labels):
        texts = sorted(groups[label])
        rng.shuffle(texts)
        split = max(4, len(texts) // 5)
        validation += [(text, y) for text in texts[:split]]
        training += [(text, y) for text in texts[split:]]
    head = Head(labels, np.zeros((len(labels), DIMENSIONS), np.float32), np.zeros(len(labels), np.float32))
    vectors = [(features(text), y) for text, y in training]
    for epoch in range(epochs):
        for i in rng.permutation(len(vectors)):
            (indices, values), y = vectors[i]
            logits = head.weights[:, indices] @ values + head.bias
            p = np.exp(logits - logits.max()); p /= p.sum(); p[y] -= 1
            rate = 0.5 / (1 + epoch * 0.03)
            head.weights[:, indices] -= rate * (p[:, None] * values[None, :] + 0.0001 * head.weights[:, indices])
            head.bias -= rate * p
    best = (float('inf'), 1)
    for temperature in [0.5, 0.75, 1, 1.5, 2, 3, 4]:
        head.temperature = temperature
        loss = np.mean([-np.log(max(head.probabilities(text)[y], 1e-9)) for text, y in validation])
        best = min(best, (float(loss), temperature))
    head.temperature = best[1]
    confusion = np.zeros((len(labels), len(labels)), dtype=int)
    accepted = correct = 0
    for text, y in validation:
        label, confidence = head.predict(text)
        predicted = labels.index(label)
        confusion[y, predicted] += 1
        if confidence >= head.threshold:
            accepted += 1; correct += int(predicted == y)
    f1 = []
    for i in range(len(labels)):
        f1.append(float(2 * confusion[i, i] / max(1, confusion[i].sum() + confusion[:, i].sum())))
    report = {'distinct_examples': len(unique), 'train': len(training), 'validation': len(validation),
              'validation_macro_f1': float(np.mean(f1)), 'validation_confusion': confusion.tolist(),
              'accepted_coverage': accepted / len(validation), 'accepted_accuracy': correct / max(1, accepted),
              'seed': seed, 'note': 'Validation also calibrates temperature; use an independent test set before deployment.'}
    return head, report


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('dataset')
    parser.add_argument('--output', required=True)
    parser.add_argument('--epochs', type=int, default=80)
    args = parser.parse_args()
    if not 1 <= args.epochs <= 1000:
        parser.error('epochs must be 1..1000')
    with open(args.dataset) as source:
        head, report = train([json.loads(line) for line in source if line.strip()], args.epochs)
    head.save(args.output, report)
    print(json.dumps(report, indent=2))
