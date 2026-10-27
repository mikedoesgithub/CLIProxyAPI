"""Persistent CPU-only s1 inference. No generative decoder or model download at runtime."""
import os
# Tiny classifier operations must not start a full BLAS thread pool.
os.environ["OPENBLAS_NUM_THREADS"] = "1"
os.environ["MKL_NUM_THREADS"] = "1"
os.environ["OMP_NUM_THREADS"] = "1"
os.environ["TOKENIZERS_PARALLELISM"] = "false"

import json
from pathlib import Path

import numpy as np

CATEGORIES = ['coding', 'agentic', 'math', 'knowledge', 'long_context', 'tool_use', 'design', 'summarisation', 'general']
DIFFICULTIES = ['trivial', 'easy', 'moderate', 'hard', 'frontier']
STAKES = ['negligible', 'low', 'medium', 'high']


def softmax(x):
    e = np.exp(x - np.max(x))
    return e / np.sum(e)


class Classifier:
    def __init__(self, model_dir, threads=2, head=None, max_tokens=128):
        import onnxruntime as ort
        from tokenizers import Tokenizer

        root = Path(model_dir)
        self.temperatures = json.loads((root / 'meta.json').read_text())['temperatures']
        self.tokenizer = Tokenizer.from_file(str(root / 'tokenizer/tokenizer.json'))
        self.tokenizer.enable_truncation(max_tokens, strategy='longest_first')
        options = ort.SessionOptions()
        options.intra_op_num_threads = threads
        options.inter_op_num_threads = 1
        options.execution_mode = ort.ExecutionMode.ORT_SEQUENTIAL
        options.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
        options.add_session_config_entry('session.intra_op.allow_spinning', '0')
        options.add_session_config_entry('session.inter_op.allow_spinning', '0')
        self.session = ort.InferenceSession(str(root / 'model.gq.onnx'), sess_options=options, providers=['CPUExecutionProvider'])
        self.head = None
        if head:
            from custom_head import Head
            self.head = Head.load(head)
        self.classify('Hello')  # Warm up before accepting traffic.

    def classify(self, request, context='(new conversation)'):
        encoded = self.tokenizer.encode(context[:600], request[-4000:])
        ids = np.asarray([encoded.ids], dtype=np.int64)
        category, difficulty, stakes, flags = self.session.run(None, {'input_ids': ids, 'attention_mask': np.ones_like(ids)})
        pc = softmax(category[0] / self.temperatures['category'])
        pd = softmax(difficulty[0] / self.temperatures['difficulty'])
        ps = softmax(stakes[0] / self.temperatures['stakes'])
        result = {'category': CATEGORIES[int(pc.argmax())], 'category_confidence': float(pc.max()),
                  'difficulty': DIFFICULTIES[int(pd.argmax())], 'stakes': STAKES[int(ps.argmax())]}
        if self.head:
            label, confidence = self.head.predict(request)
            if confidence >= self.head.threshold:
                result.update(category=label, category_confidence=confidence)
        return result


class HeadOnlyClassifier:
    """Optional ultra-small mode; requires operator-trained task and difficulty heads."""
    def __init__(self, head, difficulty_head):
        from custom_head import Head
        self.task = Head.load(head)
        self.difficulty = Head.load(difficulty_head)
        if set(self.difficulty.labels) - set(DIFFICULTIES):
            raise ValueError('difficulty head contains unknown levels')

    def classify(self, request, context='(new conversation)'):
        task, confidence = self.task.predict(request)
        difficulty, difficulty_confidence = self.difficulty.predict(request)
        if difficulty_confidence < self.difficulty.threshold:
            difficulty = 'hard'  # Conservative effort when a tiny head is uncertain.
        return {'category': task, 'category_confidence': confidence,
                'difficulty': difficulty, 'stakes': 'unknown', 'abstain': confidence < self.task.threshold}
