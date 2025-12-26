<script>
    import { _ } from "svelte-i18n";
    import tooltip from "@/actions/tooltip";
    import Field from "@/components/base/Field.svelte";
    import MimeTypeSelectOption from "@/components/base/MimeTypeSelectOption.svelte";
    import MultipleValueInput from "@/components/base/MultipleValueInput.svelte";
    import ObjectSelect from "@/components/base/ObjectSelect.svelte";
    import Toggler from "@/components/base/Toggler.svelte";
    import SchemaField from "@/components/collections/schema/SchemaField.svelte";
    import baseMimeTypesList from "@/mimes.js";
    import CommonHelper from "@/utils/CommonHelper";

    export let field;
    export let key = "";

    const isSingleOptions = [
        { label: $_("common.placeholder.single"), value: true },
        { label: $_("common.placeholder.multiple"), value: false },
    ];

    let mimeTypesList = baseMimeTypesList.slice();
    let isSingle = field.maxSelect <= 1;
    let oldIsSingle = isSingle;

    $: if (typeof field.maxSelect == "undefined") {
        loadDefaults();
    } else {
        appendMissingMimeTypes();
    }

    $: if (oldIsSingle != isSingle) {
        oldIsSingle = isSingle;
        if (isSingle) {
            field.maxSelect = 1;
        } else {
            field.maxSelect = 99;
        }
    }

    function loadDefaults() {
        field.maxSelect = 1;
        field.thumbs = [];
        field.mimeTypes = [];

        isSingle = true;
        oldIsSingle = isSingle;
    }

    // append any previously set custom mime types to the predefined
    // list for backward compatibility
    function appendMissingMimeTypes() {
        if (CommonHelper.isEmpty(field.mimeTypes)) {
            return;
        }

        const missing = [];

        for (const v of field.mimeTypes) {
            if (!!mimeTypesList.find((item) => item.mimeType === v)) {
                continue; // exist
            }

            missing.push({ mimeType: v });
        }

        if (missing.length) {
            mimeTypesList = mimeTypesList.concat(missing);
        }
    }
</script>

<SchemaField bind:field {key} on:rename on:remove on:duplicate {...$$restProps}>
    <svelte:fragment let:interactive>
        <div class="separator" />

        <Field
            class="form-field form-field-single-multiple-select {!interactive ? 'readonly' : ''}"
            inlineError
            let:uniqueId
        >
            <ObjectSelect
                id={uniqueId}
                items={isSingleOptions}
                readonly={!interactive}
                bind:keyOfSelected={isSingle}
            />
        </Field>

        <div class="separator" />
    </svelte:fragment>

    <svelte:fragment slot="options">
        <div class="grid grid-sm">
            <div class="col-sm-12">
                <Field class="form-field" name="fields.{key}.mimeTypes" let:uniqueId>
                    <label for={uniqueId}>
                        <span class="txt">{$_("common.placeholder.allowableFileType")}</span>
                    </label>
                    <ObjectSelect
                        id={uniqueId}
                        multiple
                        searchable
                        closable={false}
                        selectionKey="mimeType"
                        selectPlaceholder={$_("common.placeholder.defaultUploadingFile")}
                        items={mimeTypesList}
                        labelComponent={MimeTypeSelectOption}
                        optionComponent={MimeTypeSelectOption}
                        bind:keyOfSelected={field.mimeTypes}
                    />
                    <div class="help-block">
                        <div tabindex="0" role="button" class="inline-flex flex-gap-0">
                            <span class="txt link-primary">{$_("common.placeholder.preset")}</span>
                            <i class="ri-arrow-drop-down-fill" aria-hidden="true" />
                            <Toggler class="dropdown dropdown-sm dropdown-nowrap dropdown-left">
                                <button
                                    type="button"
                                    class="dropdown-item closable"
                                    role="menuitem"
                                    on:click={() => {
                                        field.mimeTypes = [
                                            "image/jpeg",
                                            "image/png",
                                            "image/svg+xml",
                                            "image/gif",
                                            "image/webp",
                                        ];
                                    }}
                                >
                                    <span class="txt"
                                        >{$_("common.placeholder.image")} (jpg, png, svg, gif, webp)</span
                                    >
                                </button>
                                <button
                                    type="button"
                                    class="dropdown-item closable"
                                    role="menuitem"
                                    on:click={() => {
                                        field.mimeTypes = [
                                            "application/pdf",
                                            "application/msword",
                                            "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
                                            "application/vnd.ms-excel",
                                            "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
                                        ];
                                    }}
                                >
                                    <span class="txt"
                                        >{$_("common.placeholder.document")} (pdf, doc/docx, xls/xlsx)</span
                                    >
                                </button>
                                <button
                                    type="button"
                                    class="dropdown-item closable"
                                    role="menuitem"
                                    on:click={() => {
                                        field.mimeTypes = [
                                            "video/mp4",
                                            "video/x-ms-wmv",
                                            "video/quicktime",
                                            "video/3gpp",
                                        ];
                                    }}
                                >
                                    <span class="txt"
                                        >{$_("common.placeholder.video")} (mp4, avi, mov, 3gp)</span
                                    >
                                </button>
                                <button
                                    type="button"
                                    class="dropdown-item closable"
                                    role="menuitem"
                                    on:click={() => {
                                        field.mimeTypes = [
                                            "application/zip",
                                            "application/x-7z-compressed",
                                            "application/x-rar-compressed",
                                        ];
                                    }}
                                >
                                    <span class="txt">{$_("common.placeholder.zip")} (zip, 7zip, rar)</span>
                                </button>
                            </Toggler>
                        </div>
                    </div>
                </Field>
            </div>

            <div class={!isSingle ? "col-sm-6" : "col-sm-8"}>
                <Field class="form-field" name="fields.{key}.thumbs" let:uniqueId>
                    <label for={uniqueId}>
                        <span class="txt">{$_("common.input.thumbSize.name")}</span>
                        <i
                            class="ri-information-line link-hint"
                            use:tooltip={{
                                text: $_("common.input.thumbSize.tip"),
                                position: "top",
                            }}
                        />
                    </label>
                    <MultipleValueInput
                        id={uniqueId}
                        placeholder={$_("common.input.thumbSize.placeholder")}
                        bind:value={field.thumbs}
                    />
                    <div class="help-block">
                        <span class="txt">{$_("common.placeholder.separator")}</span>
                        <button type="button" class="inline-flex flex-gap-0">
                            <span class="txt link-primary"> {$_("common.input.imageSizeEg.name")}</span>
                            <i class="ri-arrow-drop-down-fill" aria-hidden="true" />
                            <Toggler class="dropdown dropdown-sm dropdown-center dropdown-nowrap p-r-10">
                                <ul class="m-0">
                                    <li>
                                        <strong>WxH</strong>
                                        {$_("common.input.imageSizeEg.content.1")}
                                    </li>
                                    <li>
                                        <strong>WxHt</strong>
                                        {$_("common.input.imageSizeEg.content.2")}
                                    </li>
                                    <li>
                                        <strong>WxHb</strong>
                                        {$_("common.input.imageSizeEg.content.3")}
                                    </li>
                                    <li>
                                        <strong>WxHf</strong>
                                        {$_("common.input.imageSizeEg.content.4")}
                                    </li>
                                    <li>
                                        <strong>0xH</strong>
                                        {$_("common.input.imageSizeEg.content.5")}
                                    </li>
                                    <li>
                                        <strong>Wx0</strong>
                                        {$_("common.input.imageSizeEg.content.6")}
                                    </li>
                                </ul>
                            </Toggler>
                        </button>
                    </div>
                </Field>
            </div>

            <div class={!isSingle ? "col-sm-3" : "col-sm-4"}>
                <Field class="form-field" name="fields.{key}.maxSize" let:uniqueId>
                    <label for={uniqueId}>{$_("common.input.maxFileSize.name")}</label>
                    <input
                        type="number"
                        id={uniqueId}
                        step="1"
                        min="0"
                        value={field.maxSize || ""}
                        on:input={(e) => (field.maxSize = e.target.value << 0)}
                        placeholder={$_("common.input.maxFileSize.placeholder", {
                            values: { fileSize: "max ~5MB" },
                        })}
                    />
                    <div class="help-block">{$_("common.placeholder.mustBeByte")}</div>
                </Field>
            </div>

            {#if !isSingle}
                <div class="col-sm-3">
                    <Field class="form-field" name="fields.{key}.maxSelect" let:uniqueId>
                        <label for={uniqueId}>Max select</label>
                        <input
                            id={uniqueId}
                            type="number"
                            step="1"
                            min="2"
                            required
                            placeholder={$_("common.message.defaultValue", { values: { value: "single" } })}
                            bind:value={field.maxSelect}
                        />
                    </Field>
                </div>
            {/if}

            <Field class="form-field form-field-toggle" name="fields.{key}.protected" let:uniqueId>
                <input type="checkbox" id={uniqueId} bind:checked={field.protected} />
                <label for={uniqueId}>
                    <span class="txt">{$_("common.switch.protected.name")}</span>
                </label>
                <small class="txt-hint">
                    {$_("common.switch.protected.placeholder")}
                   
                </small>
            </Field>
        </div>
    </svelte:fragment>
</SchemaField>

<style>
    :global(.form-field-file-max-select) {
        width: 100px;
        flex-shrink: 0;
    }
</style>
