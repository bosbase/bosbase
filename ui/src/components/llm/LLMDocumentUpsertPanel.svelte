<script>
    import { _ } from "svelte-i18n";
    import { createEventDispatcher } from "svelte";
    import ApiClient from "@/utils/ApiClient";
    import { addSuccessToast } from "@/stores/toasts";
    import { setErrors } from "@/stores/errors";
    import OverlayPanel from "@/components/base/OverlayPanel.svelte";
    import Field from "@/components/base/Field.svelte";

    export let collection = null;

    const dispatch = createEventDispatcher();

    const metadataPlaceholder = '{"topic": "physics"}';

    let upsertPanel;
    let isEditing = false;
    let documentId = null;
    let formData = {
        id: "",
        content: "",
        metadata: "{}",
        embedding: "",
    };

    export function show(collectionData, document = null) {
        collection = collectionData;
        isEditing = !!document;
        documentId = document?.id || null;

        if (document) {
            formData.id = document.id || "";
            formData.content = document.content || "";
            formData.metadata = JSON.stringify(document.metadata || {}, null, 2);
            formData.embedding = document.embedding && document.embedding.length ? JSON.stringify(document.embedding) : "";
        } else {
            formData = {
                id: "",
                content: "",
                metadata: "{}",
                embedding: "",
            };
        }

        setErrors({});
        upsertPanel?.show();
    }

    function hide() {
        upsertPanel?.hide();
    }

    function normalizeMetadata(value) {
        if (!value || !value.trim()) {
            return {};
        }

        const parsed = JSON.parse(value);
        if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") {
            throw new Error("invalid metadata");
        }

        const normalized = {};
        Object.entries(parsed).forEach(([key, val]) => {
            normalized[key] = val === undefined || val === null ? "" : String(val);
        });

        return normalized;
    }

    function parseEmbedding(value) {
        if (!value || !value.trim()) {
            return undefined;
        }

        const parsed = JSON.parse(value);
        if (!Array.isArray(parsed) || !parsed.every((entry) => typeof entry === "number")) {
            throw new Error("invalid embedding");
        }

        return parsed;
    }

    async function save() {
        const errorsBag = {};

        let metadata;
        try {
            metadata = normalizeMetadata(formData.metadata);
        } catch (err) {
            errorsBag.metadata = $_("page.llm.documents.panel.metadataInvalid");
        }

        let embedding;
        try {
            embedding = parseEmbedding(formData.embedding);
        } catch (err) {
            errorsBag.embedding = $_("page.llm.documents.panel.embeddingInvalid");
        }

        if (!formData.content.trim() && (!embedding || embedding.length === 0)) {
            errorsBag.content = $_("page.llm.documents.panel.contentRequired");
        }

        if (Object.keys(errorsBag).length) {
            setErrors(errorsBag);
            return;
        }

        const payload = {
            content: formData.content || "",
            metadata,
        };

        if (embedding !== undefined) {
            payload.embedding = embedding;
        }

        if (!isEditing && formData.id.trim()) {
            payload.id = formData.id.trim();
        }

        try {
            if (isEditing && documentId) {
                await ApiClient.send(
                    `/api/llm-documents/${encodeURIComponent(collection.name)}/${encodeURIComponent(documentId)}`,
                    {
                        method: "PATCH",
                        body: payload,
                    },
                );
                addSuccessToast($_("page.llm.documents.toast.updated"));
            } else {
                await ApiClient.send(`/api/llm-documents/${encodeURIComponent(collection.name)}`, {
                    method: "POST",
                    body: payload,
                });
                addSuccessToast($_("page.llm.documents.toast.created"));
            }

            hide();
            dispatch("saved", { isEdit: isEditing });
        } catch (err) {
            ApiClient.error(err);
        }
    }
</script>

<OverlayPanel bind:this={upsertPanel} title={isEditing ? $_("page.llm.documents.panel.editTitle") : $_("page.llm.documents.panel.createTitle")}>
    <form on:submit|preventDefault={save}>
        <Field class="form-field" name="id" let:uniqueId>
            <label for={uniqueId}>
                <span class="txt">{$_("page.llm.documents.panel.idLabel")}</span>
            </label>
            <input
                type="text"
                id={uniqueId}
                placeholder={$_("page.llm.documents.panel.idPlaceholder")}
                bind:value={formData.id}
                disabled={isEditing}
            />
            <div class="help-block">
                <p>{$_("page.llm.documents.panel.idHelp")}</p>
            </div>
        </Field>

        <Field class="form-field" name="content" let:uniqueId>
            <label for={uniqueId}>
                <span class="txt">{$_("page.llm.documents.panel.contentLabel")}</span>
            </label>
            <textarea
                id={uniqueId}
                rows="6"
                placeholder={$_("page.llm.documents.panel.contentPlaceholder")}
                bind:value={formData.content}
            />
            <div class="help-block">
                <p>{$_("page.llm.documents.panel.contentHelp")}</p>
            </div>
        </Field>

        <Field class="form-field" name="metadata" let:uniqueId>
            <label for={uniqueId}>
                <span class="txt">{$_("page.llm.documents.panel.metadataLabel")}</span>
            </label>
            <textarea
                id={uniqueId}
                rows="6"
                placeholder={metadataPlaceholder}
                bind:value={formData.metadata}
                style="font-family: monospace; font-size: 0.9em;"
            />
            <div class="help-block">
                <p>{$_("page.llm.documents.panel.metadataHelp")}</p>
            </div>
        </Field>

        <Field class="form-field" name="embedding" let:uniqueId>
            <label for={uniqueId}>
                <span class="txt">{$_("page.llm.documents.panel.embeddingLabel")}</span>
            </label>
            <textarea
                id={uniqueId}
                rows="4"
                placeholder="[0.12, 0.98, 0.11]"
                bind:value={formData.embedding}
                style="font-family: monospace; font-size: 0.9em;"
            />
            <div class="help-block">
                <p>{$_("page.llm.documents.panel.embeddingHelp")}</p>
            </div>
        </Field>

        <div class="btns-group">
            <button type="submit" class="btn btn-expanded">
                <i class="ri-check-line" />
                <span class="txt">
                    {isEditing ? $_("page.llm.documents.panel.update") : $_("page.llm.documents.panel.create")}
                </span>
            </button>
            <button type="button" class="btn btn-outline" on:click={hide}>
                <span class="txt">{$_("page.llm.documents.panel.cancel")}</span>
            </button>
        </div>
    </form>
</OverlayPanel>
