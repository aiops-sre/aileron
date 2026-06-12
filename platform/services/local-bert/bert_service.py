#!/usr/bin/env python3
import json
import sys
from sentence_transformers import SentenceTransformer
from flask import Flask, request, jsonify
import logging

# Disable transformers warnings
logging.getLogger("transformers").setLevel(logging.ERROR)

app = Flask(__name__)
model = None

def load_model():
    global model
    try:
        print("📦 Loading local BERT model (all-MiniLM-L6-v2)...")
        model = SentenceTransformer('all-MiniLM-L6-v2')
        print("✅ BERT model loaded successfully (90MB)")
        return True
    except Exception as e:
        print(f"❌ Failed to load BERT model: {e}")
        return False

@app.route('/health', methods=['GET'])
def health():
    return jsonify({"status": "healthy", "model_loaded": model is not None})

@app.route('/embed', methods=['POST'])
def embed_text():
    if not model:
        return jsonify({"error": "Model not loaded"}), 500
    
    data = request.json
    text = data.get('text', '')
    
    try:
        embedding = model.encode(text).tolist()
        return jsonify({"embedding": embedding, "model": "all-MiniLM-L6-v2"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500

if __name__ == '__main__':
    import os
    if load_model():
        port = int(os.getenv("PORT", "8766"))
        print(f"🚀 Starting local BERT service on http://localhost:{port}")
        app.run(host='0.0.0.0', port=port, debug=False)
    else:
        sys.exit(1)
