"""
main.py
High-concurrency script execution service with directory monitoring and auto-reloading.
Uses watchdog for filesystem monitoring, automatically loads Python scripts from watched directories.
use http call function that load from directory
"""
import asyncio
import importlib.util
import sys
import os
import json
import logging
import threading
import uuid
import time
import inspect
import hashlib
import fnmatch
from pathlib import Path
from typing import Any, Dict, List, Optional, Callable, Set, Tuple, Union
from dataclasses import dataclass, field
from datetime import datetime
from enum import Enum
from concurrent.futures import ThreadPoolExecutor

# Watchdog imports
from watchdog.observers import Observer
from watchdog.events import (
    FileSystemEventHandler, 
    FileSystemEvent,
    FileCreatedEvent,
    FileModifiedEvent,
    FileDeletedEvent,
    FileMovedEvent
)

# FastAPI imports
from fastapi import FastAPI, Request, HTTPException, Depends, BackgroundTasks, Query, Body
from fastapi.responses import JSONResponse, HTMLResponse
from fastapi.middleware.cors import CORSMiddleware
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel, Field, field_validator, ConfigDict
from contextlib import asynccontextmanager

# ==================== CONFIGURATION ====================
class Config:
    """Global configuration settings"""
    # Executor settings
    MAX_WORKERS = 8
    SCRIPT_TIMEOUT = 30
    
    # Directory monitoring settings
    WATCH_DIRS = ["./scripts"]  # Default directories to watch
    WATCH_RECURSIVE = True
    WATCH_INTERVAL = 1.0
    WATCH_PATTERNS = ["*.py"]
    WATCH_IGNORE_PATTERNS = [
        "__pycache__/*", 
        "*.pyc", 
        "*.pyo", 
        "*.pyd",
        ".git/*",
        ".vscode/*",
        ".idea/*",
        "test_*.py",
        "*_test.py",
        "main.py",
        "logs/*",
        "*.venv",
        ".python-version"
    ]
    
    # Auto-reload settings
    AUTO_RELOAD = True
    RELOAD_DEBOUNCE = 0.5  # Debounce delay in seconds
    
    # Script discovery
    AUTO_DISCOVER_FUNCTIONS = True
    DEFAULT_FUNCTION_NAMES = ["main", "process", "execute", "run", "handle"]
    
    # Caching
    MAX_CACHE_SIZE = 1000
    CACHE_CLEANUP_INTERVAL = 300

# ==================== LOGGING CONFIGURATION ====================
# Only enable file logging if explicitly requested (disable in dev mode to avoid watchfiles issues)
handlers = [logging.StreamHandler()]
if os.getenv('ENABLE_FILE_LOGGING', '').lower() in ('true', '1', 'yes'):
    logs_dir = Path('logs')
    logs_dir.mkdir(exist_ok=True)
    handlers.append(logging.FileHandler(logs_dir / 'script_watcher_service.log', encoding='utf-8'))

logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s',
    handlers=handlers
)
logger = logging.getLogger(__name__)

# ==================== DATA MODELS ====================
class ScriptStatus(Enum):
    """Enumeration for script status"""
    LOADED = "loaded"
    UNLOADED = "unloaded"
    ERROR = "error"
    RELOADING = "reloading"

@dataclass
class ScriptInfo:
    """Information about a loaded script"""
    path: str
    name: str
    module: Optional[Any] = None
    functions: Dict[str, Callable] = field(default_factory=dict)
    metadata: Dict[str, Any] = field(default_factory=dict)
    status: ScriptStatus = ScriptStatus.UNLOADED
    load_time: Optional[float] = None
    last_modified: Optional[float] = None
    error_message: Optional[str] = None
    call_count: int = 0
    total_time: float = 0.0
    last_call_time: Optional[float] = None
    
    @property
    def avg_time(self) -> float:
        """Calculate average execution time"""
        return self.total_time / self.call_count if self.call_count > 0 else 0
    
    def to_dict(self) -> Dict[str, Any]:
        """Convert to dictionary for API response"""
        return {
            "name": self.name,
            "path": self.path,
            "status": self.status.value,
            "functions": list(self.functions.keys()),
            "metadata": self.metadata,
            "load_time": datetime.fromtimestamp(self.load_time).isoformat() if self.load_time else None,
            "last_modified": datetime.fromtimestamp(self.last_modified).isoformat() if self.last_modified else None,
            "call_count": self.call_count,
            "avg_time": self.avg_time,
            "last_call_time": datetime.fromtimestamp(self.last_call_time).isoformat() if self.last_call_time else None,
            "error": self.error_message
        }

# ==================== REQUEST/RESPONSE MODELS ====================
class ScriptExecuteRequest(BaseModel):
    """Request model for script execution"""
    script_path: Optional[str] = Field(None, description="Full path to the script file")
    script_name: Optional[str] = Field(None, description="Script name (for auto-discovered scripts)")
    function_name: str = Field(..., description="Name of the function to execute")
    args: List[Any] = Field([], description="Positional arguments for the function")
    kwargs: Dict[str, Any] = Field({}, description="Keyword arguments for the function")
    timeout: Optional[int] = Field(None, description="Custom timeout in seconds")
    
    @field_validator('function_name')
    @classmethod
    def validate_function_name(cls, v):
        """Validate function name"""
        if not v or not v.strip():
            raise ValueError('Function name cannot be empty')
        return v.strip()
    
    model_config = ConfigDict(
        json_schema_extra={
            "example": {
                "script_name": "data_processor",
                "function_name": "process_data",
                "args": [1, 2, 3],
                "kwargs": {"param1": "value1"}
            }
        }
    )

class DirectoryWatchRequest(BaseModel):
    """Request model for adding a directory to watch"""
    directory: str = Field(..., description="Directory path to watch")
    recursive: bool = Field(True, description="Watch subdirectories recursively")
    patterns: List[str] = Field(["*.py"], description="File patterns to watch")

# ==================== WATCHDOG EVENT HANDLER ====================
class ScriptFileEventHandler(FileSystemEventHandler):
    """Watchdog event handler for script file changes"""
    
    def __init__(self, executor: 'ScriptExecutor'):
        self.executor = executor
        self._reload_queue: Dict[str, float] = {}
        self._reload_lock = threading.Lock()
        self._last_events: Dict[str, float] = {}
        self._event_debounce = Config.RELOAD_DEBOUNCE
    
    def _should_handle(self, file_path: str) -> bool:
        """Check if this file should be handled"""
        # Check file patterns
        if not any(fnmatch.fnmatch(file_path, pattern) for pattern in Config.WATCH_PATTERNS):
            return False
        
        # Check ignore patterns
        if any(fnmatch.fnmatch(file_path, pattern) for pattern in Config.WATCH_IGNORE_PATTERNS):
            return False
        
        # Check file extension
        if not file_path.endswith('.py'):
            return False
        
        return True
    
    def _schedule_reload(self, script_path: str, event_type: str):
        """Schedule script reload with debouncing"""
        current_time = time.time()
        
        with self._reload_lock:
            # Check if within debounce period
            if script_path in self._last_events:
                time_since_last = current_time - self._last_events[script_path]
                if time_since_last < self._event_debounce:
                    logger.debug(f"Skipping duplicate event: {script_path} ({time_since_last:.2f}s ago)")
                    return
            
            self._last_events[script_path] = current_time
            
            # Add to reload queue
            self._reload_queue[script_path] = current_time
            logger.debug(f"Scheduled reload: {script_path} ({event_type})")
            
            # Process reload in separate thread after debounce delay
            threading.Timer(
                self._event_debounce,
                self._process_reload_queue,
                args=[script_path]
            ).start()
    
    def _process_reload_queue(self, script_path: str):
        """Process the reload queue"""
        with self._reload_lock:
            if script_path not in self._reload_queue:
                return
            
            event_time = self._reload_queue.pop(script_path)
            time_since_event = time.time() - event_time
            
            if time_since_event >= self._event_debounce:
                try:
                    if os.path.exists(script_path):
                        logger.info(f"Auto-reloading script: {script_path}")
                        self.executor.reload_script(script_path)
                    else:
                        logger.info(f"Script deleted, unloading: {script_path}")
                        self.executor.unload_script(script_path)
                except Exception as e:
                    logger.error(f"Auto-reload failed for {script_path}: {e}")
    
    def on_created(self, event: FileSystemEvent):
        """Handle file creation events"""
        if not event.is_directory:
            if self._should_handle(event.src_path):
                logger.info(f"New script detected: {event.src_path}")
                self._schedule_reload(event.src_path, "created")
    
    def on_modified(self, event: FileSystemEvent):
        """Handle file modification events"""
        if not event.is_directory:
            if self._should_handle(event.src_path):
                logger.info(f"Script modified: {event.src_path}")
                self._schedule_reload(event.src_path, "modified")
    
    def on_deleted(self, event: FileSystemEvent):
        """Handle file deletion events"""
        if not event.is_directory:
            if self._should_handle(event.src_path):
                logger.info(f"Script deleted: {event.src_path}")
                self.executor.unload_script(event.src_path)
    
    def on_moved(self, event: FileSystemEvent):
        """Handle file move events"""
        if not event.is_directory:
            # Source file
            if self._should_handle(event.src_path):
                logger.info(f"Script moved: {event.src_path} -> {event.dest_path}")
                self.executor.unload_script(event.src_path)
            
            # Destination file
            if self._should_handle(event.dest_path):
                self._schedule_reload(event.dest_path, "moved")

# ==================== SCRIPT EXECUTOR ====================
class ScriptExecutor:
    """
    Thread-safe script executor with directory monitoring.
    Manages script loading, unloading, and execution with isolated contexts.
    """
    
    def __init__(self):
        self._lock = threading.RLock()
        self._thread_local = threading.local()
        
        # Thread pool for script execution
        self._executor = ThreadPoolExecutor(
            max_workers=Config.MAX_WORKERS,
            thread_name_prefix="ScriptWorker"
        )
        
        # Script management
        self._scripts: Dict[str, ScriptInfo] = {}
        self._name_to_path: Dict[str, str] = {}
        
        # Directory monitoring
        self._observer = Observer(timeout=Config.WATCH_INTERVAL)
        self._event_handler = ScriptFileEventHandler(self)
        self._watching_dirs: Set[str] = set()
        
        # Statistics
        self._start_time = time.time()
        self._total_requests = 0
        self._success_requests = 0
        self._failed_requests = 0
        
        logger.info("ScriptExecutor initialized")
    
    def start_watching(self) -> bool:
        """Start directory monitoring service"""
        try:
            if not self._observer.is_alive():
                self._observer.start()
                logger.info("Directory monitoring service started")
                
                # Initial scan of all watched directories
                self._scan_all_directories()
                
            return True
        except Exception as e:
            logger.error(f"Failed to start directory monitoring: {e}")
            return False
    
    def stop_watching(self) -> bool:
        """Stop directory monitoring service"""
        try:
            if self._observer.is_alive():
                self._observer.stop()
                self._observer.join()
                logger.info("Directory monitoring service stopped")
            return True
        except Exception as e:
            logger.error(f"Failed to stop directory monitoring: {e}")
            return False
    
    def add_watch_directory(self, directory: str, recursive: bool = True) -> bool:
        """Add a directory to watch"""
        with self._lock:
            abs_dir = os.path.abspath(directory)
            
            if not os.path.exists(abs_dir):
                # Auto-create directory
                try:
                    os.makedirs(abs_dir, exist_ok=True)
                    logger.info(f"Created directory: {abs_dir}")
                except Exception as e:
                    logger.error(f"Failed to create directory {abs_dir}: {e}")
                    return False
            
            if abs_dir in self._watching_dirs:
                logger.warning(f"Directory already being watched: {abs_dir}")
                return True
            
            try:
                # Add to observer
                self._observer.schedule(
                    self._event_handler,
                    abs_dir,
                    recursive=recursive
                )
                
                self._watching_dirs.add(abs_dir)
                
                # Scan directory for scripts
                script_count = self._scan_directory(abs_dir, recursive)
                
                logger.info(f"Added watch directory: {abs_dir} (recursive={recursive}, scripts={script_count})")
                return True
                
            except Exception as e:
                logger.error(f"Failed to add watch directory {abs_dir}: {e}")
                return False
    
    def remove_watch_directory(self, directory: str) -> bool:
        """Remove a directory from watch"""
        with self._lock:
            abs_dir = os.path.abspath(directory)
            
            if abs_dir not in self._watching_dirs:
                logger.warning(f"Directory not being watched: {abs_dir}")
                return False
            
            try:
                # Stop watching
                self._observer.unschedule(abs_dir)
                
                # Unload all scripts in this directory
                self._unload_scripts_in_directory(abs_dir)
                
                # Remove from watching set
                self._watching_dirs.remove(abs_dir)
                
                logger.info(f"Removed watch directory: {abs_dir}")
                return True
                
            except Exception as e:
                logger.error(f"Failed to remove watch directory {abs_dir}: {e}")
                return False
    
    def _scan_all_directories(self):
        """Scan all watched directories for scripts"""
        for dir_path in self._watching_dirs:
            self._scan_directory(dir_path, recursive=True)
    
    def _scan_directory(self, directory: str, recursive: bool = True) -> int:
        """Scan directory for Python scripts and load them"""
        script_count = 0
        
        try:
            if recursive:
                for root, dirs, files in os.walk(directory):
                    # Filter ignored directories
                    dirs[:] = [d for d in dirs if not self._should_ignore_path(os.path.join(root, d))]
                    
                    for file in files:
                        file_path = os.path.join(root, file)
                        if self._should_load_file(file_path):
                            if self._load_script(file_path):
                                script_count += 1
            else:
                for item in os.listdir(directory):
                    item_path = os.path.join(directory, item)
                    if os.path.isfile(item_path) and self._should_load_file(item_path):
                        if self._load_script(item_path):
                            script_count += 1
            
            logger.info(f"Directory scan completed: {directory} (found {script_count} scripts)")
            return script_count
            
        except Exception as e:
            logger.error(f"Directory scan failed for {directory}: {e}")
            return 0
    
    def _should_ignore_path(self, path: str) -> bool:
        """Check if path should be ignored"""
        for pattern in Config.WATCH_IGNORE_PATTERNS:
            if fnmatch.fnmatch(path, pattern):
                return True
        return False
    
    def _should_load_file(self, file_path: str) -> bool:
        """Check if file should be loaded as a script"""
        # Check extension
        if not file_path.endswith('.py'):
            return False
        
        # Check ignore patterns
        if self._should_ignore_path(file_path):
            return False
        
        # Check file patterns
        for pattern in Config.WATCH_PATTERNS:
            if fnmatch.fnmatch(file_path, pattern):
                return True
        
        return False
    
    def _load_script(self, script_path: str, force: bool = False) -> bool:
        """Load a script file into memory"""
        with self._lock:
            # Check if file exists
            if not os.path.exists(script_path):
                if script_path in self._scripts:
                    self.unload_script(script_path)
                return False
            
            try:
                # Get file information
                mtime = os.path.getmtime(script_path)
                script_name = os.path.splitext(os.path.basename(script_path))[0]
                
                # Check if reload is needed
                if script_path in self._scripts:
                    script_info = self._scripts[script_path]
                    if not force and script_info.status == ScriptStatus.LOADED:
                        if script_info.last_modified == mtime:
                            return True  # Already loaded and not modified
                
                # Dynamically import the module
                module_name = f"script_{hashlib.md5(script_path.encode()).hexdigest()[:8]}"
                spec = importlib.util.spec_from_file_location(module_name, script_path)
                
                if spec is None:
                    raise ImportError(f"Could not create module spec for: {script_path}")
                
                module = importlib.util.module_from_spec(spec)
                
                # Add custom attributes
                module.__dict__['__script_info__'] = {
                    'path': script_path,
                    'name': script_name,
                    'load_time': time.time()
                }
                
                # Execute module code
                spec.loader.exec_module(module)
                
                # Discover functions in the module
                functions = {}
                if Config.AUTO_DISCOVER_FUNCTIONS:
                    for name in dir(module):
                        if name.startswith('_'):
                            continue
                        
                        obj = getattr(module, name)
                        if callable(obj):
                            # Check function signature
                            try:
                                sig = inspect.signature(obj)
                                is_method = False
                                
                                # Check if it's a method (has self or cls parameter)
                                params = list(sig.parameters.keys())
                                if params and params[0] in ['self', 'cls']:
                                    is_method = True
                                
                                if not is_method or (is_method and hasattr(obj, '__self__')):
                                    functions[name] = obj
                            except:
                                functions[name] = obj
                
                # Try default function names if no functions discovered
                if not functions:
                    for func_name in Config.DEFAULT_FUNCTION_NAMES:
                        if hasattr(module, func_name):
                            obj = getattr(module, func_name)
                            if callable(obj):
                                functions[func_name] = obj
                
                # Create script info object
                script_info = ScriptInfo(
                    path=script_path,
                    name=script_name,
                    module=module,
                    functions=functions,
                    metadata={
                        'module_name': module_name,
                        'file_size': os.path.getsize(script_path),
                        'functions_count': len(functions),
                        'functions_list': list(functions.keys()),
                        'line_count': self._count_lines(script_path)
                    },
                    status=ScriptStatus.LOADED,
                    load_time=time.time(),
                    last_modified=mtime
                )
                
                # Save to cache
                self._scripts[script_path] = script_info
                self._name_to_path[script_name] = script_path
                
                logger.info(f"Loaded script: {script_name} (functions: {len(functions)})")
                return True
                
            except Exception as e:
                logger.error(f"Failed to load script {script_path}: {e}", exc_info=True)
                
                # Save error information
                script_info = ScriptInfo(
                    path=script_path,
                    name=script_name if 'script_name' in locals() else Path(script_path).stem,
                    status=ScriptStatus.ERROR,
                    error_message=str(e),
                    last_modified=mtime if 'mtime' in locals() else None
                )
                self._scripts[script_path] = script_info
                return False
    
    def _count_lines(self, file_path: str) -> int:
        """Count lines in a file"""
        try:
            with open(file_path, 'r', encoding='utf-8') as f:
                return sum(1 for _ in f)
        except:
            return 0
    
    def _unload_scripts_in_directory(self, directory: str):
        """Unload all scripts in a directory"""
        scripts_to_unload = []
        
        for path, script_info in self._scripts.items():
            if path.startswith(directory):
                scripts_to_unload.append(path)
        
        for path in scripts_to_unload:
            self.unload_script(path)
    
    def unload_script(self, script_path: str) -> bool:
        """Unload a script from memory"""
        with self._lock:
            if script_path in self._scripts:
                script_info = self._scripts[script_path]
                script_name = script_info.name
                
                # Clean up name mapping
                if script_name in self._name_to_path:
                    del self._name_to_path[script_name]
                
                # Remove from script cache
                del self._scripts[script_path]
                
                # Clean up module references
                if script_info.module:
                    module_name = script_info.module.__name__
                    if module_name in sys.modules:
                        del sys.modules[module_name]
                        logger.debug(f"Cleaned up module: {module_name}")
                
                logger.info(f"Unloaded script: {script_path}")
                return True
            return False
    
    def reload_script(self, script_path: str) -> bool:
        """Reload a script from disk"""
        return self._load_script(script_path, force=True)
    
    def get_script(self, script_path: Optional[str] = None, script_name: Optional[str] = None) -> Optional[ScriptInfo]:
        """Get script information by path or name"""
        with self._lock:
            if script_path:
                return self._scripts.get(script_path)
            elif script_name:
                if script_name in self._name_to_path:
                    return self._scripts.get(self._name_to_path[script_name])
            return None
    
    def list_scripts(self) -> List[Dict[str, Any]]:
        """List all loaded scripts"""
        with self._lock:
            return [script.to_dict() for script in self._scripts.values()]
    
    def list_directories(self) -> List[str]:
        """List all watched directories"""
        return list(self._watching_dirs)
    
    def execute_script(
        self, 
        script_path: Optional[str] = None,
        script_name: Optional[str] = None,
        function_name: str = "",
        args: tuple = (),
        kwargs: Optional[Dict] = None
    ) -> Dict[str, Any]:
        """
        Execute a script function.
        
        Args:
            script_path: Full path to the script file
            script_name: Script name (for auto-discovered scripts)
            function_name: Name of the function to execute (required)
            args: Positional arguments for the function
            kwargs: Keyword arguments for the function
        
        Returns:
            Dictionary with execution results
        """
        if kwargs is None:
            kwargs = {}
        
        if not function_name:
            return {
                'success': False,
                'error': 'Function name is required',
                'execution_time': 0
            }
        
        request_id = str(uuid.uuid4())
        start_time = time.time()
        
        try:
            # Get script information
            script_info = self.get_script(script_path, script_name)
            
            # If script not found but script_path is provided, try to load it
            if not script_info and script_path:
                # Normalize the path to absolute
                abs_script_path = os.path.abspath(script_path)
                if os.path.exists(abs_script_path):
                    logger.info(f"Script not in cache, attempting to load: {abs_script_path}")
                    if self._load_script(abs_script_path):
                        script_info = self.get_script(abs_script_path, script_name)
            
            if not script_info:
                return {
                    'success': False,
                    'request_id': request_id,
                    'error': f'Script not found: {script_name or script_path}',
                    'execution_time': time.time() - start_time
                }
            
            # Check if function exists
            if function_name not in script_info.functions:
                return {
                    'success': False,
                    'request_id': request_id,
                    'error': f'Function not found: {function_name}',
                    'available_functions': list(script_info.functions.keys()),
                    'execution_time': time.time() - start_time
                }
            
            # Get the function
            func = script_info.functions[function_name]
            
            # Execute in thread pool
            future = self._executor.submit(func, *args, **kwargs)
            
            try:
                # Set timeout
                timeout = Config.SCRIPT_TIMEOUT
                result = future.result(timeout=timeout)
                
                # Update statistics
                exec_time = time.time() - start_time
                with self._lock:
                    script_info.call_count += 1
                    script_info.total_time += exec_time
                    script_info.last_call_time = time.time()
                
                return {
                    'success': True,
                    'request_id': request_id,
                    'result': result,
                    'execution_time': exec_time,
                    'script': script_info.name,
                    'function': function_name
                }
                
            except TimeoutError:
                future.cancel()
                return {
                    'success': False,
                    'request_id': request_id,
                    'error': f'Execution timeout ({timeout} seconds)',
                    'execution_time': timeout
                }
            
        except Exception as e:
            logger.error(f"Script execution failed: {e}", exc_info=True)
            return {
                'success': False,
                'request_id': request_id,
                'error': str(e),
                'execution_time': time.time() - start_time
            }
    
    async def execute_script_async(
        self,
        script_path: Optional[str] = None,
        script_name: Optional[str] = None,
        function_name: str = "",
        args: tuple = (),
        kwargs: Optional[Dict] = None
    ) -> Dict[str, Any]:
        """Asynchronously execute a script function"""
        loop = asyncio.get_event_loop()
        return await loop.run_in_executor(
            None,
            lambda: self.execute_script(
                script_path, script_name, function_name, args, kwargs
            )
        )

# ==================== FASTAPI APPLICATION ====================
# Global executor instance
_executor = None

@asynccontextmanager
async def lifespan(app: FastAPI):
    """Application lifecycle management"""
    global _executor
    
    # Startup
    logger.info("Application starting...")
    _executor = ScriptExecutor()
    
    # Add default watch directories
    for watch_dir in Config.WATCH_DIRS:
        if os.path.exists(watch_dir) or os.path.isdir(watch_dir):
            _executor.add_watch_directory(watch_dir, Config.WATCH_RECURSIVE)
    
    # Start monitoring
    _executor.start_watching()
    
    yield
    
    # Shutdown
    logger.info("Application shutting down...")
    _executor.stop_watching()
    _executor._executor.shutdown(wait=True)
    logger.info("Application shut down complete")

app = FastAPI(
    title="Script Execution and Directory Monitoring Service",
    description="High-concurrency script execution service with automatic script reloading",
    version="2.0.0",
    lifespan=lifespan
)

# CORS middleware
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

# Dependency injection
def get_executor():
    return _executor

# ==================== API ENDPOINTS ====================
@app.get("/", response_class=HTMLResponse)
async def root():
    """Root endpoint with service information"""
    return """
    functions service
    """

@app.get("/health")
async def health_check(executor: ScriptExecutor = Depends(get_executor)):
    """Health check endpoint with service metrics"""
    return {
        "status": "healthy",
        "uptime": time.time() - executor._start_time,
        "scripts_loaded": len(executor._scripts),
        "directories_watching": len(executor._watching_dirs),
        "total_requests": executor._total_requests,
        "success_requests": executor._success_requests,
        "failed_requests": executor._failed_requests,
        "timestamp": datetime.now().isoformat()
    }

@app.get("/scripts")
async def list_scripts(executor: ScriptExecutor = Depends(get_executor)):
    """Get all loaded scripts"""
    return {
        "scripts": executor.list_scripts(),
        "count": len(executor._scripts)
    }

@app.get("/scripts/{script_name}")
async def get_script_info(
    script_name: str,
    executor: ScriptExecutor = Depends(get_executor)
):
    """Get detailed information about a specific script"""
    script_info = executor.get_script(script_name=script_name)
    if not script_info:
        raise HTTPException(status_code=404, detail="Script not found")
    
    return script_info.to_dict()

@app.post("/execute")
async def execute_script(
    request: ScriptExecuteRequest,
    executor: ScriptExecutor = Depends(get_executor)
):
    """
    Execute a script function.
    
    The function name is required and passed as a parameter in the request body.
    """
    try:
        # Update request statistics
        executor._total_requests += 1
        
        # Execute the script
        result = await executor.execute_script_async(
            script_path=request.script_path,
            script_name=request.script_name,
            function_name=request.function_name,
            args=tuple(request.args),
            kwargs=request.kwargs
        )
        
        # Update success/failure statistics
        if result['success']:
            executor._success_requests += 1
        else:
            executor._failed_requests += 1
        
        return result
        
    except Exception as e:
        executor._failed_requests += 1
        raise HTTPException(status_code=500, detail=str(e))

@app.get("/directories")
async def list_directories(executor: ScriptExecutor = Depends(get_executor)):
    """List all watched directories"""
    return {
        "directories": executor.list_directories(),
        "count": len(executor._watching_dirs)
    }

@app.post("/directories")
async def add_directory(
    request: DirectoryWatchRequest,
    executor: ScriptExecutor = Depends(get_executor)
):
    """Add a directory to watch"""
    success = executor.add_watch_directory(
        request.directory, 
        request.recursive
    )
    if not success:
        raise HTTPException(status_code=400, detail="Failed to add directory")
    
    return {"success": True, "directory": request.directory}

@app.delete("/directories/{directory_path:path}")
async def remove_directory(
    directory_path: str,
    executor: ScriptExecutor = Depends(get_executor)
):
    """Remove a directory from watch"""
    success = executor.remove_watch_directory(directory_path)
    if not success:
        raise HTTPException(status_code=404, detail="Directory not found or failed to remove")
    
    return {"success": True, "directory": directory_path}

@app.post("/scripts/{script_name}/reload")
async def reload_script(
    script_name: str,
    executor: ScriptExecutor = Depends(get_executor)
):
    """Manually reload a script"""
    script_info = executor.get_script(script_name=script_name)
    if not script_info:
        raise HTTPException(status_code=404, detail="Script not found")
    
    success = executor.reload_script(script_info.path)
    return {"success": success, "script": script_name}

@app.post("/scripts/reload-all")
async def reload_all_scripts(executor: ScriptExecutor = Depends(get_executor)):
    """Reload all scripts"""
    reloaded = 0
    failed = 0
    
    with executor._lock:
        scripts = list(executor._scripts.keys())
        for script_path in scripts:
            if executor.reload_script(script_path):
                reloaded += 1
            else:
                failed += 1
    
    return {
        "success": True,
        "reloaded": reloaded,
        "failed": failed,
        "total": len(scripts)
    }

@app.get("/metrics")
async def get_metrics(executor: ScriptExecutor = Depends(get_executor)):
    """Get detailed service metrics"""
    with executor._lock:
        total_calls = sum(s.call_count for s in executor._scripts.values())
        total_time = sum(s.total_time for s in executor._scripts.values())
        avg_time = total_time / total_calls if total_calls > 0 else 0
        
        return {
            "scripts": {
                "total": len(executor._scripts),
                "loaded": len([s for s in executor._scripts.values() if s.status == ScriptStatus.LOADED]),
                "error": len([s for s in executor._scripts.values() if s.status == ScriptStatus.ERROR]),
            },
            "executions": {
                "total": total_calls,
                "success": executor._success_requests,
                "failed": executor._failed_requests,
                "avg_time": avg_time
            },
            "directories": {
                "watching": len(executor._watching_dirs),
            },
            "performance": {
                "uptime": time.time() - executor._start_time,
                "requests_per_second": executor._total_requests / (time.time() - executor._start_time) if time.time() > executor._start_time else 0
            }
        }
