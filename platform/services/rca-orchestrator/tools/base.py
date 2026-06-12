from __future__ import annotations
import time
from abc import ABC, abstractmethod
from typing import Any


class BaseTool(ABC):
    name: str
    description: str
    parameters: dict  # JSON Schema for parameters

    @abstractmethod
    async def execute(self, **kwargs) -> str:
        pass

    def to_ollama_tool(self) -> dict:
        return {
            "type": "function",
            "function": {
                "name": self.name,
                "description": self.description,
                "parameters": self.parameters,
            },
        }

    async def run(self, **kwargs) -> tuple[str, int]:
        """Returns (result_str, duration_ms). Exceptions propagate so the DAG engine
        can route them to its errors dict rather than treating them as valid outputs."""
        t0 = time.monotonic()
        result = await self.execute(**kwargs)
        duration_ms = int((time.monotonic() - t0) * 1000)
        return result, duration_ms
