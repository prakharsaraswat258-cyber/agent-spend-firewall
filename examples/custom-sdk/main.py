"""Meter and cap your own script with Pitstop.

1. Start the proxy:   pitstop serve
2. Run this script:   python main.py      (needs `pip install openai` and OPENAI_API_KEY)

The official OpenAI SDK talks to Pitstop instead of api.openai.com. Pitstop
prices the call, checks your caps before sending it, and records the cost under
the run named in the X-Pitstop-Run header. Your key passes through and is never
stored. See the run with `pitstop runs`.

For a provider from providers.json (OpenRouter, Gemini, ...), use
base_url="http://127.0.0.1:4546/p/<name>" and that provider's key.
"""

from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:4546/v1",
    default_headers={"X-Pitstop-Run": "custom-sdk-demo"},  # any name; groups calls into one run
)

resp = client.chat.completions.create(
    model="gpt-5-mini",
    max_completion_tokens=200,  # also bounds what Pitstop reserves before the call
    messages=[{"role": "user", "content": "Say hello in five words."}],
)
print(resp.choices[0].message.content)
print("tokens:", resp.usage.prompt_tokens, "in,", resp.usage.completion_tokens, "out")
