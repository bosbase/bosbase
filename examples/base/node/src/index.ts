import Database from "better-sqlite3";
import {
  ShapeStream,
  type Message,
  isChangeMessage,
  isControlMessage,
  Offset,
} from "@electric-sql/client";

// Type for our items table
type ItemRow = {
  id: number;
  name: string;
  description: string | null;
};

interface PreparedStatements {
  insert: Database.Statement;
  update: Database.Statement;
  delete: Database.Statement;
  saveState: Database.Statement;
  getState: Database.Statement;
}

class SyncManager {
  private db: Database.Database;
  private stream: ShapeStream<ItemRow>;
  private prepared: PreparedStatements;

  constructor() {
    // Initialize SQLite database
    this.db = new Database("../pb_data/data.db");
    this.initializeDatabase();

    // Prepare all our SQL statements
    this.prepared = {
      insert: this.db.prepare(
        "INSERT OR REPLACE INTO items (id, name, description) VALUES (?, ?, ?)"
      ),
      update: this.db.prepare(
        "UPDATE items SET name = ?, description = ? WHERE id = ?"
      ),
      delete: this.db.prepare("DELETE FROM items WHERE id = ?"),
      saveState: this.db.prepare(
        "INSERT OR REPLACE INTO sync_state (table_name, last_offset, shape_handle) VALUES (?, ?, ?)"
      ),
      getState: this.db.prepare(
        "SELECT last_offset, shape_handle FROM sync_state WHERE table_name = ?"
      ),
    };

    const syncState = this.getSyncState();

    // Initialize ElectricSQL stream
    this.stream = new ShapeStream({
      url: "https://elc.aogobo.com/v1/shape",
      params: {
        table: "items",
        replica: "full", // Get full rows on updates
      },
      headers: {
        Authorization: async () =>
          `Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxIiwibmFtZSI6InNhbSIsImlzcyI6ImFmbyIsImlhdCI6MTc1OTExMjU1MCwiZXhwIjoxOTE2NzkyNTUwfQ.4Eybf1S4IiMFxk2RKOutL0zu3a2WhF4n35a_cpKHodY`,
      },
      // Start from last known sync point if available
      offset: syncState.offset,
      handle: syncState.handle,
    });
  }

  private initializeDatabase() {
    // Create items table matching Postgres schema
    this.db.exec(`
      CREATE TABLE IF NOT EXISTS items (
        id INTEGER PRIMARY KEY,
        name TEXT NOT NULL,
        description TEXT
      );
    `);

    // Create offset and shape handle tracking table
    this.db.exec(`
      CREATE TABLE IF NOT EXISTS sync_state (
        table_name TEXT PRIMARY KEY,
        last_offset TEXT NOT NULL,
        shape_handle TEXT
      );
    `);

    // Ensure legacy databases include the shape_handle column
    const syncStateColumns = (this.db
      .prepare("PRAGMA table_info(sync_state)")
      .all() as { name: string }[]);
    const hasShapeHandle = syncStateColumns.some(
      (column) => column.name === "shape_handle"
    );
    if (!hasShapeHandle) {
      this.db.exec("ALTER TABLE sync_state ADD COLUMN shape_handle TEXT");
    }
  }

  private getSyncState(): { offset?: Offset; handle?: string } {
    const result = this.prepared.getState?.get("items") as
      | { last_offset: string; shape_handle: string | null }
      | undefined;

    if (!result) {
      return {};
    }

    const offset = result.last_offset as Offset;
    const handle = result.shape_handle ?? undefined;

    if (offset !== undefined && offset !== `-1` && !handle) {
      return {};
    }

    return { offset, handle };
  }

  private saveSyncState() {
    const offset = this.stream.lastOffset;
    const handle = this.stream.shapeHandle;

    if (offset !== undefined && offset !== `-1` && !handle) {
      return;
    }

    this.prepared.saveState?.run("items", offset, handle ?? null);
  }

  private handleMessages(
    messages: Message<ItemRow>[],
    transaction: (statements: [Database.Statement, any[]][]) => void,
    currentBatch: [Database.Statement, any[]][]
  ): [Database.Statement, any[]][] {
    for (const msg of messages) {
      if (isChangeMessage<ItemRow>(msg)) {
        const { headers, value } = msg;

        switch (headers.operation) {
          case "insert":
            currentBatch.push([
              this.prepared.insert!,
              [value.id, value.name, value.description],
            ]);
            break;
          case "update":
            currentBatch.push([
              this.prepared.update!,
              [value.name, value.description, value.id],
            ]);
            break;
          case "delete":
            currentBatch.push([this.prepared.delete!, [value.id]]);
            break;
        }
        this.saveSyncState();
      } else if (isControlMessage(msg)) {
        if (msg.headers.control === "up-to-date") {
          // Execute current batch in transaction and clear it
          if (currentBatch.length > 0) {
            transaction(currentBatch);
            currentBatch = [];
          }
          this.saveSyncState();
          console.log("Sync is up to date!");
        }
      }
    }
    return currentBatch;
  }

  async start() {
    console.log("Starting sync manager...");

    // Create a transaction function that we'll use to wrap our operations
    let transaction = this.db.transaction(
      (statements: [Database.Statement, any[]][]) => {
        for (const [stmt, params] of statements) {
          stmt.run(...params);
        }
      }
    );

    let currentBatch: [Database.Statement, any[]][] = [];

    this.stream.subscribe(
      (messages) => {
        currentBatch = this.handleMessages(messages, transaction, currentBatch);
      },
      (error: Error) => {
        console.error("Error in sync:", error);
        // Clear the batch on error
        currentBatch = [];
      }
    );

    console.log("Sync manager is running...");
  }

  close() {
    this.stream.unsubscribeAll();
    this.db.close();
  }
}

// Start the sync manager
const syncManager = new SyncManager();
await syncManager.start();

// Handle shutdown gracefully
process.on("SIGINT", () => {
  console.log("Shutting down...");
  syncManager.close();
  process.exit(0);
});
