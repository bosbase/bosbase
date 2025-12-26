from __future__ import annotations

import asyncio
import json
from typing import Any, Dict, List, Optional

from fastapi import FastAPI, Header, Query, WebSocket, WebSocketDisconnect
from fastapi.responses import StreamingResponse
from pydantic import BaseModel

app = FastAPI()

tasks: Dict[str, Dict[str, Any]] = {}
task_counter = 0


class TaskPayload(BaseModel):
    title: str
    priority: Optional[str] = None
    status: str = "pending"


class TaskUpdate(BaseModel):
    title: Optional[str] = None
    priority: Optional[str] = None
    status: Optional[str] = None


def next_task_id() -> str:
    global task_counter
    task_counter += 1
    return str(task_counter)


@app.get("/health")
def health():
    return {"status": "ok", "tasks": len(tasks)}


@app.post("/embed")
def embed(text: str):
    return {"embedding": [0.1, 0.2, 0.3]}


@app.post("/tasks")
def create_task(payload: TaskPayload, x_plugin_key: Optional[str] = Header(default=None)):
    task_id = next_task_id()
    task = {"id": task_id, **payload.model_dump()}
    tasks[task_id] = task
    return {"task": task, "received_key": x_plugin_key}


@app.patch("/tasks/{task_id}")
def update_task(task_id: str, payload: TaskUpdate):
    existing = tasks.get(task_id, {"id": task_id})
    updates = payload.model_dump(exclude_none=True)
    updated_task = {**existing, **updates, "id": task_id}
    tasks[task_id] = updated_task
    return {"task": updated_task}


@app.delete("/tasks/{task_id}")
def delete_task(task_id: str):
    tasks.pop(task_id, None)
    return {"deleted": task_id}


@app.get("/reports/summary")
def report_summary(
    since: Optional[str] = Query(default=None),
    limit: int = Query(default=50),
    tags: List[str] = Query(default_factory=list),
    x_plugin_key: Optional[str] = Header(default=None),
):
    return {
        "since": since,
        "limit": limit,
        "tags": tags,
        "tasks_seen": len(tasks),
        "received_key": x_plugin_key,
    }


@app.options("/tasks")
def options_tasks():
    return {"allow": ["GET", "POST", "PATCH", "DELETE", "OPTIONS"]}


async def _event_stream(topic: str, x_plugin_key: Optional[str]):
    for idx in range(3):
        payload = {
            "topic": topic or "default",
            "sequence": idx,
            "plugin_key": x_plugin_key,
        }
        yield f"data: {json.dumps(payload)}\n\n"
        await asyncio.sleep(0.2)

    yield "event: end\ndata: stream-complete\n\n"


@app.get("/events/updates")
async def stream_updates(
    topic: str = Query(default="general"),
    x_plugin_key: Optional[str] = Header(default=None),
):
    return StreamingResponse(
        _event_stream(topic, x_plugin_key), media_type="text/event-stream"
    )


@app.websocket("/ws/chat")
async def websocket_chat(websocket: WebSocket):
    await websocket.accept()

    room = websocket.query_params.get("room", "lobby")
    plugin_key = websocket.headers.get("x-plugin-key")

    await websocket.send_json({"kind": "welcome", "room": room, "plugin_key": plugin_key})

    try:
        while True:
            raw_message = await websocket.receive_text()
            try:
                message = json.loads(raw_message)
            except json.JSONDecodeError:
                message = {"raw": raw_message}

            await websocket.send_json(
                {
                    "kind": "echo",
                    "room": room,
                    "received": message,
                    "plugin_key": plugin_key,
                }
            )
    except WebSocketDisconnect:
        await websocket.close()
