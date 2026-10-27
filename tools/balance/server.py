#!/usr/bin/env python3
"""Loopback-only decision service. One inference at a time; busy requests fail fast."""
import argparse
import json
import socket
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from classifier import Classifier, HeadOnlyClassifier


def create_server(classifier, port):
    inference = threading.BoundedSemaphore(1)
    connections = threading.BoundedSemaphore(8)

    class Server(ThreadingHTTPServer):
        daemon_threads = True
        request_queue_size = 8

        def process_request(self, request, address):
            if not connections.acquire(blocking=False):
                self.shutdown_request(request)
                return
            try:
                super().process_request(request, address)
            except Exception:
                connections.release()
                raise

        def process_request_thread(self, request, address):
            try:
                super().process_request_thread(request, address)
            finally:
                connections.release()

        def handle_error(self, request, address):
            # Deliberately omit request bodies and tracebacks from service logs.
            pass

    class Handler(BaseHTTPRequestHandler):
        protocol_version = 'HTTP/1.0'

        def setup(self):
            super().setup()
            self.connection.settimeout(2)  # Local control traffic only, never generation.

        def log_message(self, *_):
            pass

        def respond(self, status, body):
            data = json.dumps(body, allow_nan=False).encode()
            try:
                self.send_response(status)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(data)))
                self.end_headers()
                self.wfile.write(data)
            except (BrokenPipeError, ConnectionResetError, socket.timeout):
                pass

        def do_GET(self):
            if self.path != '/health':
                self.respond(404, {'error': 'not found'})
                return
            self.respond(200, {'ready': True, 'backend': 'lexical-heads' if isinstance(classifier, HeadOnlyClassifier) else 's1-onnx-cpu'})

        def do_POST(self):
            if self.path != '/classify':
                self.respond(404, {'error': 'not found'})
                return
            if not inference.acquire(blocking=False):
                self.respond(503, {'error': 'busy'})
                return
            try:
                length = int(self.headers.get('Content-Length', '0'))
                if not 0 < length <= 65536 or self.headers.get('Transfer-Encoding'):
                    self.respond(413, {'error': 'invalid body length'})
                    return
                data = json.loads(self.rfile.read(length))
                text, context = data.get('request'), data.get('context', '(new conversation)')
                if not isinstance(text, str) or not isinstance(context, str) or len(text) > 4000 or len(context) > 600:
                    self.respond(400, {'error': 'invalid input'})
                    return
                self.respond(200, classifier.classify(text, context))
            except (ValueError, AttributeError, UnicodeError):
                self.respond(400, {'error': 'invalid JSON'})
            except Exception:
                self.respond(500, {'error': 'classification failed'})
            finally:
                inference.release()

    return Server(('127.0.0.1', port), Handler)


def serve(classifier, port):
    with create_server(classifier, port) as server:
        print(f'Balance ready on http://127.0.0.1:{port}; CPU threads bounded; prompt logging disabled', flush=True)
        server.serve_forever()


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--model-dir')
    parser.add_argument('--head-only', action='store_true', help='Use only trained lexical heads; no ONNX model')
    parser.add_argument('--difficulty-head')
    parser.add_argument('--port', type=int, default=8318)
    parser.add_argument('--max-tokens', type=int, choices=[128, 256, 512], default=128)
    parser.add_argument('--threads', type=int, choices=range(1, 5), default=2)
    parser.add_argument('--head', help='Optional locally trained task head JSON')
    args = parser.parse_args()
    if args.head_only:
        if not args.head or not args.difficulty_head:
            parser.error('--head-only requires --head and --difficulty-head')
        classifier = HeadOnlyClassifier(args.head, args.difficulty_head)
    else:
        if not args.model_dir:
            parser.error('--model-dir is required for s1')
        classifier = Classifier(args.model_dir, args.threads, args.head, args.max_tokens)
    serve(classifier, args.port)
