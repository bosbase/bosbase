<script>
    import { _ } from "svelte-i18n";
    import ApiClient from "@/utils/ApiClient";
    import { addSuccessToast, addErrorToast } from "@/stores/toasts";
    import { setErrors } from "@/stores/errors";
    import OverlayPanel from "@/components/base/OverlayPanel.svelte";
    import Field from "@/components/base/Field.svelte";
    import { createEventDispatcher } from "svelte";

    const dispatch = createEventDispatcher();

    export let collection = null;

    let importPanel;
    let importData = "";
    let skipDuplicates = false;
    let isImporting = false;

    export function show(collectionData) {
        collection = collectionData;
        importData = "";
        skipDuplicates = false;
        setErrors({});
        importPanel?.show();
    }

    function hide() {
        importPanel?.hide();
    }

    function handleFileSelect(event) {
        const file = event.target.files[0];
        if (!file) return;

        const reader = new FileReader();
        reader.onload = (e) => {
            importData = e.target.result;
        };
        reader.readAsText(file);
    }

    async function importVectors() {
        if (!importData || importData.trim() === "") {
            setErrors({ importData: $_("page.vector.import.dataRequired") });
            return;
        }

        let documents;
        try {
            documents = JSON.parse(importData);
            if (!Array.isArray(documents)) {
                throw new Error("Data must be an array of documents");
            }
        } catch (err) {
            setErrors({ importData: $_("page.vector.import.dataInvalid", { message: err.message }) });
            return;
        }

        if (documents.length === 0) {
            setErrors({ importData: $_("page.vector.import.dataEmpty") });
            return;
        }

        // Validate documents structure
        for (let i = 0; i < documents.length; i++) {
            const doc = documents[i];
            if (!doc.vector || !Array.isArray(doc.vector)) {
                setErrors({ importData: $_("page.vector.import.dataInvalidVector", { index: i }) });
                return;
            }
        }

        isImporting = true;
        setErrors({});

        try {
            const response = await ApiClient.send(`/api/vectors/${encodeURIComponent(collection.name)}/documents/batch`, {
                method: "POST",
                body: {
                    documents: documents,
                    skipDuplicates: skipDuplicates,
                },
            });

            if (response.failedCount > 0) {
                addErrorToast($_("page.vector.import.toast.partial", {
                    inserted: response.insertedCount,
                    failed: response.failedCount
                }));
                if (response.errors && response.errors.length > 0) {
                    console.error("Import errors:", response.errors);
                }
            } else {
                addSuccessToast($_("page.vector.import.toast.success", { count: response.insertedCount }));
            }

            hide();
            dispatch("imported");
        } catch (err) {
            ApiClient.error(err);
        } finally {
            isImporting = false;
        }
    }

    $: exampleData = JSON.stringify([
        {
            "id": "doc1",
            "vector": new Array(collection?.dimension || 384).fill(0).map(() => Math.random()),
            "content": "Introduction to neural networks and deep learning architectures",
            "metadata": {
                "category": "technology",
                "tags": ["AI", "machine learning", "neural networks"],
                "author": "John Doe",
                "date": "2024-01-15",
                "type": "article"
            }
        },
        {
            "id": "doc2",
            "vector": new Array(collection?.dimension || 384).fill(0).map(() => Math.random()),
            "content": "Best practices for vector database optimization and indexing strategies",
            "metadata": {
                "category": "tutorial",
                "tags": ["database", "optimization"],
                "difficulty": "intermediate",
                "views": 1250
            }
        },
        {
            "id": "doc3",
            "vector": new Array(collection?.dimension || 384).fill(0).map(() => Math.random()),
            "content": "",
            "metadata": {
                "category": "data",
                "source": "external_api",
                "processed": true
            }
        }
    ], null, 2);
</script>

<OverlayPanel bind:this={importPanel} title={$_("page.vector.import.title")}>
    <form on:submit|preventDefault={importVectors}>
        <Field class="form-field" name="file" let:uniqueId>
            <label for={uniqueId}>
                <span class="txt">{$_("page.vector.import.fileLabel")}</span>
            </label>
            <input
                type="file"
                id={uniqueId}
                accept=".json,application/json"
                on:change={handleFileSelect}
            />
            <div class="help-block">
                <p>{$_("page.vector.import.fileHelp")}</p>
            </div>
        </Field>

        <Field class="form-field required" name="importData" let:uniqueId>
            <label for={uniqueId}>
                <span class="txt">{$_("page.vector.import.dataLabel")}</span>
            </label>
            <textarea
                id={uniqueId}
                rows="10"
                required
                placeholder={$_("page.vector.import.dataPlaceholder")}
                bind:value={importData}
                style="font-family: monospace; font-size: 0.85em;"
            />
            <div class="help-block">
                <p>{$_("page.vector.import.dataHelp")}</p>
                <ul class="txt-xs txt-hint m-t-xs" style="margin-left: 1.5rem;">
                    <li><code>{$_("page.vector.import.dataHelpId")}</code></li>
                    <li><code>{$_("page.vector.import.dataHelpVector")}</code></li>
                    <li><code>{$_("page.vector.import.dataHelpContent")}</code></li>
                    <li><code>{$_("page.vector.import.dataHelpMetadata")}</code></li>
                </ul>
                <p class="m-t-sm">
                    <button type="button" class="btn btn-xs btn-outline" on:click={() => importData = exampleData}>
                        <i class="ri-file-code-line" />
                        <span class="txt">{$_("page.vector.import.loadExample")}</span>
                    </button>
                </p>
            </div>
        </Field>

        <Field class="form-field" name="skipDuplicates" let:uniqueId>
            <label for={uniqueId} class="checkbox-label">
                <input
                    type="checkbox"
                    id={uniqueId}
                    bind:checked={skipDuplicates}
                />
                <span class="txt">{$_("page.vector.import.skipDuplicatesLabel")}</span>
            </label>
        </Field>

        <div class="btns-group">
            <button type="submit" class="btn btn-expanded" disabled={isImporting}>
                <i class="ri-upload-line" />
                <span class="txt">{isImporting ? $_("page.vector.import.importingButton") : $_("page.vector.import.importButton")}</span>
            </button>
            <button type="button" class="btn btn-outline" on:click={hide}>
                <span class="txt">{$_("common.action.cancel")}</span>
            </button>
        </div>
    </form>
</OverlayPanel>

<style>
    .checkbox-label {
        display: flex;
        align-items: center;
        gap: 0.5rem;
        cursor: pointer;
    }
</style>

