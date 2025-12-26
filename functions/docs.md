Example script - Data processing functions
"""
import time
import json
import random
from typing import Dict, List, Any

def process_data(data: Dict[str, Any]) -> Dict[str, Any]:
    """Process data and add metadata"""
    if isinstance(data, dict):
        result = {
            **data,
            "processed": True,
            "timestamp": time.time(),
            "random_value": random.random(),
            "processor": "data_processor.py"
        }
        return result
    else:
        return {"error": "Invalid input", "input": data}

def calculate_sum(numbers: List[float]) -> Dict[str, Any]:
    """Calculate sum of numbers"""
    return {
        "sum": sum(numbers), 
        "count": len(numbers),
        "average": sum(numbers) / len(numbers) if numbers else 0
    }

def transform_text(text: str, operation: str = "uppercase") -> str:
    """Transform text based on operation"""
    if operation == "uppercase":
        return text.upper()
    elif operation == "lowercase":
        return text.lower()
    elif operation == "reverse":
        return text[::-1]
    else:
        return text

def batch_process(items: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
    """Process a batch of items"""
    return [process_data(item) for item in items]

# Note: There's no 'main' function - all functions are individually callable

Math operations script
"""
import math

def add(a: float, b: float) -> float:
    """Add two numbers"""
    return a + b

def multiply(a: float, b: float) -> float:
    """Multiply two numbers"""
    return a * b

def factorial(n: int) -> int:
    """Calculate factorial"""
    if n < 0:
        raise ValueError("Factorial not defined for negative numbers")
    return math.factorial(n)

def fibonacci(n: int) -> int:
    """Calculate nth Fibonacci number"""
    if n <= 0:
        return 0
    elif n == 1:
        return 1
    else:
        a, b = 0, 1
        for _ in range(2, n + 1):
            a, b = b, a + b
        return b
'''