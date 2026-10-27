#!/usr/bin/env python3
"""Local warm inference measurements; does not claim accuracy or target-server speed."""
import argparse
import json
import platform
import resource
import statistics
import sys
import time

from classifier import Classifier

REQUESTS = [
    'Hello, how are you?',
    'Use the browser to compare these products and add the cheapest to my cart.',
    'Write a Blender Python script that models a detailed spiral staircase with railings.',
    'Prove that the square root of two is irrational.',
    'Refactor this Go service to eliminate a data race without changing its API.',
    'Summarize the following article in three bullet points.',
    'Debug a distributed consensus protocol under network partitions and prove safety.',
]

if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--model-dir', required=True)
    parser.add_argument('--threads', type=int, choices=range(1, 5), default=2)
    parser.add_argument('--max-tokens', type=int, choices=[128, 256, 512], default=128)
    parser.add_argument('--long', action='store_true', help='Stress token limit with long requests')
    parser.add_argument('--iterations', type=int, default=70)
    args = parser.parse_args()
    model = Classifier(args.model_dir, args.threads, max_tokens=args.max_tokens)
    timings, predictions = [], []
    for i in range(args.iterations):
        text = REQUESTS[i % len(REQUESTS)]
        if args.long:
            text = (text + " ") * 100
        start = time.perf_counter()
        prediction = model.classify(text)
        timings.append((time.perf_counter() - start) * 1000)
        if i < len(REQUESTS):
            predictions.append({'request': text, **prediction})
    values = sorted(timings)
    print(json.dumps({'platform': platform.platform(), 'threads': args.threads, 'max_tokens': args.max_tokens, 'long_inputs': args.long, 'iterations': len(values),
                      'warm_p50_ms': statistics.median(values), 'warm_p95_ms': values[int((len(values)-1)*0.95)],
                      'max_rss_mib': resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / (1024*1024 if sys.platform == 'darwin' else 1024),
                      'predictions': predictions}, indent=2))
