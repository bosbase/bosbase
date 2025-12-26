#!/usr/bin/env python3
"""
Test script for HTTP calls to the FastAPI service
Run with: uv run test_http.py
Or: python test_http.py
"""
import json
import time
import sys
from typing import Dict, Any

try:
    import requests
    HTTP_CLIENT = 'requests'
except ImportError:
    try:
        import httpx
        HTTP_CLIENT = 'httpx'
    except ImportError:
        print("ERROR: Neither 'requests' nor 'httpx' is available.")
        print("Install one with: uv add requests")
        sys.exit(1)

BASE_URL = "http://localhost:8000"

def print_section(title: str):
    """Print a formatted section header"""
    print("\n" + "=" * 70)
    print(f"  {title}")
    print("=" * 70)

def print_test(name: str):
    """Print a test name"""
    print(f"\n📋 Test: {name}")
    print("-" * 70)

def _get(url: str, **kwargs):
    """HTTP GET request"""
    if HTTP_CLIENT == 'requests':
        return requests.get(url, **kwargs)
    else:
        return httpx.get(url, **kwargs)

def _post(url: str, **kwargs):
    """HTTP POST request"""
    if HTTP_CLIENT == 'requests':
        return requests.post(url, **kwargs)
    else:
        return httpx.post(url, **kwargs)

def test_health() -> bool:
    """Test health endpoint"""
    print_section("Health Check")
    try:
        response = _get(f"{BASE_URL}/health", timeout=5)
        if response.status_code == 200:
            data = response.json()
            print(f"✅ Status: {data.get('status', 'unknown')}")
            print(f"   Uptime: {data.get('uptime', 0):.2f} seconds")
            print(f"   Scripts loaded: {data.get('scripts_loaded', 0)}")
            print(f"   Directories watching: {data.get('directories_watching', 0)}")
            return True
        else:
            print(f"❌ Status: {response.status_code}")
            return False
    except (requests.exceptions.ConnectionError if HTTP_CLIENT == 'requests' else httpx.ConnectError):
        print("❌ ERROR: Could not connect to server")
        print("   Make sure FastAPI is running: uv run fastapi dev")
        return False
    except Exception as e:
        print(f"❌ ERROR: {e}")
        return False

def test_list_scripts() -> bool:
    """Test listing all scripts"""
    print_section("List All Scripts")
    try:
        response = _get(f"{BASE_URL}/scripts", timeout=5)
        if response.status_code == 200:
            data = response.json()
            scripts = data.get('scripts', [])
            print(f"✅ Found {len(scripts)} script(s):\n")
            for script in scripts:
                print(f"   📄 {script.get('name', 'unknown')}")
                print(f"      Status: {script.get('status', 'unknown')}")
                print(f"      Functions: {', '.join(script.get('functions', []))}")
                print()
            return True
        else:
            print(f"❌ Status: {response.status_code}")
            return False
    except Exception as e:
        print(f"❌ ERROR: {e}")
        return False

def test_execute_function(
    script_name: str, 
    function_name: str, 
    args: list = None, 
    kwargs: dict = None,
    expected_result: Any = None
) -> bool:
    """Test executing a function"""
    print_test(f"{script_name}.{function_name}")
    
    payload = {
        "script_name": script_name,
        "function_name": function_name,
        "args": args or [],
        "kwargs": kwargs or {}
    }
    
    try:
        print(f"   Request: {json.dumps(payload, indent=6)}")
        response = _post(
            f"{BASE_URL}/execute", 
            json=payload,
            timeout=10
        )
        
        if response.status_code == 200:
            result = response.json()
            if result.get('success'):
                print(f"   ✅ Success!")
                print(f"   Result: {json.dumps(result.get('result'), indent=6)}")
                print(f"   Execution time: {result.get('execution_time', 0):.6f}s")
                
                if expected_result is not None:
                    if result.get('result') == expected_result:
                        print(f"   ✅ Expected result matches!")
                    else:
                        print(f"   ⚠️  Expected: {expected_result}, Got: {result.get('result')}")
                
                return True
            else:
                print(f"   ❌ Execution failed: {result.get('error', 'Unknown error')}")
                return False
        else:
            print(f"   ❌ HTTP {response.status_code}: {response.text}")
            return False
    except Exception as e:
        print(f"   ❌ ERROR: {e}")
        return False

def test_math_operations() -> int:
    """Test all math operations"""
    print_section("Math Operations Tests")
    passed = 0
    total = 0
    
    # Test add
    total += 1
    if test_execute_function("math_operations", "add", args=[10, 20], expected_result=30):
        passed += 1
    
    # Test multiply
    total += 1
    if test_execute_function("math_operations", "multiply", args=[5, 7], expected_result=35):
        passed += 1
    
    # Test factorial
    total += 1
    if test_execute_function("math_operations", "factorial", args=[5], expected_result=120):
        passed += 1
    
    # Test fibonacci
    total += 1
    if test_execute_function("math_operations", "fibonacci", args=[10], expected_result=55):
        passed += 1
    
    print(f"\n📊 Math Operations: {passed}/{total} tests passed")
    return passed, total

def test_data_processing() -> tuple:
    """Test all data processing functions"""
    print_section("Data Processing Tests")
    passed = 0
    total = 0
    
    # Test process_data
    total += 1
    result = test_execute_function(
        "data_processor",
        "process_data",
        kwargs={"data": {"name": "test", "value": 123}}
    )
    if result:
        passed += 1
    
    # Test calculate_sum
    total += 1
    expected = {"sum": 12.0, "count": 4, "average": 3.0}
    result = test_execute_function(
        "data_processor",
        "calculate_sum",
        args=[[1.5, 2.5, 3.5, 4.5]],
        expected_result=expected
    )
    if result:
        passed += 1
    
    # Test transform_text - uppercase
    total += 1
    if test_execute_function(
        "data_processor",
        "transform_text",
        args=["Hello World"],
        kwargs={"operation": "uppercase"},
        expected_result="HELLO WORLD"
    ):
        passed += 1
    
    # Test transform_text - reverse
    total += 1
    if test_execute_function(
        "data_processor",
        "transform_text",
        args=["Hello World"],
        kwargs={"operation": "reverse"},
        expected_result="dlroW olleH"
    ):
        passed += 1
    
    # Test batch_process
    total += 1
    result = test_execute_function(
        "data_processor",
        "batch_process",
        args=[[
            {"id": 1, "name": "item1"},
            {"id": 2, "name": "item2"}
        ]]
    )
    if result:
        passed += 1
    
    print(f"\n📊 Data Processing: {passed}/{total} tests passed")
    return passed, total

def test_metrics() -> bool:
    """Test metrics endpoint"""
    print_section("Service Metrics")
    try:
        response = _get(f"{BASE_URL}/metrics", timeout=5)
        if response.status_code == 200:
            data = response.json()
            print("✅ Metrics retrieved:\n")
            print(f"   Scripts: {data.get('scripts', {})}")
            print(f"   Executions: {data.get('executions', {})}")
            print(f"   Directories: {data.get('directories', {})}")
            print(f"   Performance: {data.get('performance', {})}")
            return True
        else:
            print(f"❌ Status: {response.status_code}")
            return False
    except Exception as e:
        print(f"❌ ERROR: {e}")
        return False

def main():
    """Run all tests"""
    print("\n" + "🚀" * 35)
    print("  FastAPI Script Execution Service - HTTP Test Suite")
    print("🚀" * 35)
    
    # Wait a bit for server to be ready
    print("\n⏳ Waiting for server to be ready...")
    time.sleep(1)
    
    results = {
        'health': False,
        'list_scripts': False,
        'math_operations': (0, 0),
        'data_processing': (0, 0),
        'metrics': False
    }
    
    # Test 1: Health check
    results['health'] = test_health()
    if not results['health']:
        print("\n❌ Server is not available. Please start the server first:")
        print("   cd /home/darkmoon/Documents/rbosbase/sasspb/functions")
        print("   uv run fastapi dev")
        sys.exit(1)
    
    # Test 2: List scripts
    results['list_scripts'] = test_list_scripts()
    
    # Test 3: Math operations
    results['math_operations'] = test_math_operations()
    
    # Test 4: Data processing
    results['data_processing'] = test_data_processing()
    
    # Test 5: Metrics
    results['metrics'] = test_metrics()
    
    # Summary
    print_section("Test Summary")
    total_passed = 0
    total_tests = 0
    
    if results['health']:
        print("✅ Health check: PASSED")
        total_passed += 1
    else:
        print("❌ Health check: FAILED")
    total_tests += 1
    
    if results['list_scripts']:
        print("✅ List scripts: PASSED")
        total_passed += 1
    else:
        print("❌ List scripts: FAILED")
    total_tests += 1
    
    math_passed, math_total = results['math_operations']
    total_passed += math_passed
    total_tests += math_total
    print(f"{'✅' if math_passed == math_total else '⚠️ '} Math operations: {math_passed}/{math_total} passed")
    
    data_passed, data_total = results['data_processing']
    total_passed += data_passed
    total_tests += data_total
    print(f"{'✅' if data_passed == data_total else '⚠️ '} Data processing: {data_passed}/{data_total} passed")
    
    if results['metrics']:
        print("✅ Metrics: PASSED")
        total_passed += 1
    else:
        print("❌ Metrics: FAILED")
    total_tests += 1
    
    print(f"\n{'='*70}")
    print(f"  Total: {total_passed}/{total_tests} tests passed")
    print(f"{'='*70}\n")
    
    if total_passed == total_tests:
        print("🎉 All tests passed!")
        sys.exit(0)
    else:
        print("⚠️  Some tests failed")
        sys.exit(1)

if __name__ == "__main__":
    main()

