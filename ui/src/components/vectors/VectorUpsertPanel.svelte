<script>
    import { _ } from "svelte-i18n";
    import ApiClient from "@/utils/ApiClient";
    import { addSuccessToast } from "@/stores/toasts";
    import { setErrors } from "@/stores/errors";
    import OverlayPanel from "@/components/base/OverlayPanel.svelte";
    import Field from "@/components/base/Field.svelte";
    import { createEventDispatcher } from "svelte";

    const dispatch = createEventDispatcher();

    export let collection = null;

    let upsertPanel;
    let isEditing = false;
    let vectorId = null;
    let formData = {
        id: "",
        vector: "",
        content: "",
        metadata: "{}",
    };

    export function show(collectionData, vector = null) {
        collection = collectionData;
        isEditing = !!vector;
        vectorId = vector?.id || null;
        
        if (vector) {
            // Editing existing vector
            formData.id = vector.id || "";
            formData.vector = JSON.stringify(vector.vector || []);
            formData.content = vector.content || "";
            formData.metadata = JSON.stringify(vector.metadata || {}, null, 2);
        } else {
            // Adding new vector
            formData.id = "";
            formData.vector = "";
            formData.content = "";
            formData.metadata = "{}";
        }
        
        setErrors({});
        upsertPanel?.show();
    }

    function hide() {
        upsertPanel?.hide();
    }

    async function save() {
        setErrors({});

        // Validate
        if (!formData.vector || formData.vector.trim() === "") {
            setErrors({ vector: $_("page.vector.upsert.vectorRequired") });
            return;
        }

        let vectorArray;
        try {
            vectorArray = JSON.parse(formData.vector);
            if (!Array.isArray(vectorArray)) {
                throw new Error("Vector must be an array");
            }
        } catch (err) {
            setErrors({ vector: $_("page.vector.upsert.vectorInvalid") });
            return;
        }

        let metadata;
        try {
            metadata = JSON.parse(formData.metadata || "{}");
        } catch (err) {
            setErrors({ metadata: $_("page.vector.upsert.metadataInvalid") });
            return;
        }

        const payload = {
            vector: vectorArray,
            content: formData.content || "",
            metadata: metadata,
        };

        if (formData.id && formData.id.trim()) {
            payload.id = formData.id.trim();
        }

        try {
            if (isEditing && vectorId) {
                // Update
                await ApiClient.send(`/api/vectors/${encodeURIComponent(collection.name)}/${encodeURIComponent(vectorId)}`, {
                    method: "PATCH",
                    body: payload,
                });
                addSuccessToast($_("page.vector.upsert.toast.updated"));
            } else {
                // Insert
                await ApiClient.send(`/api/vectors/${encodeURIComponent(collection.name)}`, {
                    method: "POST",
                    body: payload,
                });
                addSuccessToast($_("page.vector.upsert.toast.added"));
            }
            
            hide();
            dispatch("saved");
        } catch (err) {
            ApiClient.error(err);
        }
    }
</script>

<OverlayPanel bind:this={upsertPanel} title={isEditing ? $_("page.vector.upsert.editTitle") : $_("page.vector.upsert.addTitle")}>
    <form on:submit|preventDefault={save}>
        <Field class="form-field" name="id" let:uniqueId>
            <label for={uniqueId}>
                <span class="txt">{$_("page.vector.upsert.idLabel")}</span>
            </label>
            <input
                type="text"
                id={uniqueId}
                placeholder={$_("page.vector.upsert.idPlaceholder")}
                bind:value={formData.id}
                disabled={isEditing}
            />
            <div class="help-block">
                <p>{$_("page.vector.upsert.idHelp")}</p>
            </div>
        </Field>

        <Field class="form-field required" name="vector" let:uniqueId>
            <label for={uniqueId}>
                <span class="txt">{$_("page.vector.upsert.vectorLabel")}</span>
            </label>
            <textarea
                id={uniqueId}
                rows="4"
                required
                placeholder={$_("page.vector.upsert.vectorPlaceholder")}
                bind:value={formData.vector}
                style="font-family: monospace; font-size: 0.9em;"
            />
            <div class="help-block">
                <p>{$_("page.vector.upsert.vectorHelp", { dimension: collection?.dimension || 'N/A' })}</p>
            </div>
        </Field>

        <Field class="form-field" name="content" let:uniqueId>
            <label for={uniqueId}>
                <span class="txt">{$_("page.vector.upsert.contentLabel")}</span>
            </label>
            <textarea
                id={uniqueId}
                rows="3"
                placeholder={$_("page.vector.upsert.contentPlaceholder")}
                bind:value={formData.content}
            />
            <div class="help-block">
                <p>{$_("page.vector.upsert.contentHelp")}</p>
                <p class="txt-xs txt-hint m-t-xs">
                    <strong>{$_("page.vector.upsert.contentExample")}</strong> "{$_("page.vector.upsert.contentExampleText")}"
                </p>
            </div>
        </Field>

        <Field class="form-field" name="metadata" let:uniqueId>
            <label for={uniqueId}>
                <span class="txt">{$_("page.vector.upsert.metadataLabel")}</span>
            </label>
            <textarea
                id={uniqueId}
                rows="6"
                placeholder={$_("page.vector.upsert.metadataPlaceholder")}
                bind:value={formData.metadata}
                style="font-family: monospace; font-size: 0.9em;"
            />
            <div class="help-block">
                <p>{$_("page.vector.upsert.metadataHelp")}</p>
                <p class="txt-xs txt-hint m-t-xs">
                    <strong>{$_("page.vector.upsert.metadataExample")}</strong>
                    <code class="block m-t-xs" style="font-size: 0.85em; padding: 0.5rem; background: var(--bg-secondary); border-radius: 4px;">
                        {'{'}<br/>
                        &nbsp;&nbsp;"category": "technology",<br/>
                        &nbsp;&nbsp;"tags": ["AI", "machine learning"],<br/>
                        &nbsp;&nbsp;"author": "John Doe",<br/>
                        &nbsp;&nbsp;"date": "2024-01-15"<br/>
                        {'}'}
                    </code>
                </p>
            </div>
        </Field>

        <div class="btns-group">
            <button type="submit" class="btn btn-expanded">
                <i class="ri-check-line" />
                <span class="txt">{isEditing ? $_("page.vector.upsert.updateButton") : $_("page.vector.upsert.addButton")}</span>
            </button>
            <button type="button" class="btn btn-outline" on:click={hide}>
                <span class="txt">{$_("common.action.cancel")}</span>
            </button>
        </div>
    </form>
</OverlayPanel>

