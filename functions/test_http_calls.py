"""
Test script for HTTP calls to the FastAPI service
"""
import requests
import json
import time

BASE_URL = "http://localhost:8000"

def test_health():
    """Test health endpoint"""
    print("=" * 60)
    print("Testing /health endpoint")
    print("=" * 60)
    response = requests.get(f"{BASE_URL}/health")
    print(f"Status: {response.status_code}")
    print(f"Response: {json.dumps(response.json(), indent=2)}")
    print()

def test_list_scripts():
    """Test listing all scripts"""
    print("=" * 60)
    print("Testing /scripts endpoint")
    print("=" * 60)
    response = requests.get(f"{BASE_URL}/scripts")
    print(f"Status: {response.status_code}")
    print(f"Response: {json.dumps(response.json(), indent=2)}")
    print()

def test_execute_function(script_name: str, function_name: str, args: list = None, kwargs: dict = None):
    """Test executing a function"""
    print("=" * 60)
    print(f"Testing /execute - {script_name}.{function_name}")
    print("=" * 60)
    
    payload = {
        "script_name": script_name,
        "function_name": function_name,
        "args": args or [],
        "kwargs": kwargs or {}
    }
    
    print(f"Request: {json.dumps(payload, indent=2)}")
    response = requests.post(f"{BASE_URL}/execute", json=payload)
    print(f"Status: {response.status_code}")
    print(f"Response: {json.dumps(response.json(), indent=2)}")
    print()
    return response.json()

def main():
    """Run all tests"""
    print("\n" + "=" * 60)
    print("FastAPI Script Execution Service - HTTP Test Suite")
    print("=" * 60 + "\n")
    
    # Wait a bit for server to be ready
    print("Waiting for server to be ready...")
    time.sleep(2)
    
    try:
        # Test 1: Health check
        test_health()
        
        # Test 2: List scripts
        test_list_scripts()
        
        # Test 3: Math operations
        print("\n" + "=" * 60)
        print("Testing Math Operations Script")
        print("=" * 60 + "\n")
        
        test_execute_function("math_operations", "add", args=[10, 20])
        test_execute_function("math_operations", "multiply", args=[5, 7])
        test_execute_function("math_operations", "factorial", args=[5])
        test_execute_function("math_operations", "fibonacci", args=[10])
        
        # Test 4: Data processing
        print("\n" + "=" * 60)
        print("Testing Data Processing Script")
        print("=" * 60 + "\n")
        
        test_execute_function(
            "data_processor", 
            "process_data", 
            kwargs={"data": {"name": "test", "value": 123}}
        )
        
        test_execute_function(
            "data_processor",
            "calculate_sum",
            args=[[1.5, 2.5, 3.5, 4.5]]
        )
        
        test_execute_function(
            "data_processor",
            "transform_text",
            args=["Hello World"],
            kwargs={"operation": "uppercase"}
        )
        
        test_execute_function(
            "data_processor",
            "transform_text",
            args=["Hello World"],
            kwargs={"operation": "reverse"}
        )
        
        test_execute_function(
            "data_processor",
            "batch_process",
            args=[[
                {"id": 1, "name": "item1"},
                {"id": 2, "name": "item2"},
                {"id": 3, "name": "item3"}
            ]]
        )
        
        # Test 5: Get metrics
        print("\n" + "=" * 60)
        print("Testing /metrics endpoint")
        print("=" * 60)
        response = requests.get(f"{BASE_URL}/metrics")
        print(f"Status: {response.status_code}")
        print(f"Response: {json.dumps(response.json(), indent=2)}")
        print()
        
        print("\n" + "=" * 60)
        print("All tests completed!")
        print("=" * 60 + "\n")
        
    except requests.exceptions.ConnectionError:
        print("ERROR: Could not connect to server. Make sure FastAPI is running on http://localhost:8000")
        print("Start the server with: uv run fastapi dev")
    except Exception as e:
        print(f"ERROR: {e}")
        import traceback
        traceback.print_exc()

if __name__ == "__main__":
    main()

