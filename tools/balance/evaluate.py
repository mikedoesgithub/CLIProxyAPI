#!/usr/bin/env python3
"""Evaluate private, independently labeled JSONL. Prints aggregates, never prompts."""
import argparse
import collections
import json

from classifier import Classifier, HeadOnlyClassifier


def evaluate(classifier, rows, threshold=.65):
    counts = collections.Counter()
    confusion = collections.defaultdict(collections.Counter)
    for row in rows:
        text, label = row['text'], row['label']
        prediction = classifier.classify(text, row.get('context', '(new conversation)'))
        guessed = prediction['category']
        counts['examples'] += 1
        confusion[label][guessed] += 1
        counts['correct'] += int(label == guessed)
        if not prediction.get('abstain', False) and prediction['category_confidence'] >= threshold:
            counts['accepted'] += 1
            counts['accepted_correct'] += int(label == guessed)
        if 'difficulty' in row:
            counts['difficulty_labeled'] += 1
            counts['difficulty_correct'] += int(row['difficulty'] == prediction['difficulty'])
    if not counts['examples']:
        raise ValueError('dataset is empty')
    return {'examples': counts['examples'], 'category_accuracy': counts['correct'] / counts['examples'],
            'accepted_coverage': counts['accepted'] / counts['examples'],
            'accepted_accuracy': counts['accepted_correct'] / max(1, counts['accepted']),
            'difficulty_examples': counts['difficulty_labeled'],
            'difficulty_accuracy': counts['difficulty_correct'] / max(1, counts['difficulty_labeled']),
            'category_confusion': dict(confusion)}


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('dataset')
    parser.add_argument('--model-dir')
    parser.add_argument('--head')
    parser.add_argument('--difficulty-head')
    parser.add_argument('--head-only', action='store_true')
    parser.add_argument('--max-tokens', type=int, choices=[128, 256, 512], default=128)
    args = parser.parse_args()
    if args.head_only:
        if not args.head or not args.difficulty_head:
            parser.error('--head-only requires --head and --difficulty-head')
        classifier = HeadOnlyClassifier(args.head, args.difficulty_head)
    else:
        if not args.model_dir:
            parser.error('--model-dir required for s1')
        classifier = Classifier(args.model_dir, head=args.head, max_tokens=args.max_tokens)
    with open(args.dataset) as source:
        report = evaluate(classifier, [json.loads(line) for line in source if line.strip()])
    print(json.dumps(report, indent=2))
