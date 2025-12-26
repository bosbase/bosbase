<script>
    import { _ } from "svelte-i18n";
    import tooltip from "@/actions/tooltip";
    import Field from "@/components/base/Field.svelte";
    import SchemaField from "@/components/collections/schema/SchemaField.svelte";

    export let field;
    export let key = "";
    // 🐱当前数值是在此处（前端）硬编码的，应该从后端获取
    let minLength = "0";
    let maxLength = "5000";
</script>

<SchemaField bind:field {key} on:rename on:remove on:duplicate {...$$restProps}>
    <svelte:fragment slot="options">
        <div class="grid grid-sm">
            <div class="col-sm-6">
                <Field class="form-field" name="fields.{key}.min" let:uniqueId>
                    <label for={uniqueId}>
                        <span class="txt">{$_("common.input.minLength.name")}</span>
                        <i
                            class="ri-information-line link-hint"
                            use:tooltip={$_("common.input.minLength.tip")}
                        />
                    </label>
                    <input
                        type="number"
                        id={uniqueId}
                        step="1"
                        min="0"
                        placeholder={$_("common.input.minLength.placeholder", {
                            values: { minLength: minLength },
                        })}
                        value={field.min || ""}
                        on:input={(e) => (field.min = e.target.value << 0)}
                    />
                </Field>
            </div>

            <div class="col-sm-6">
                <Field class="form-field" name="fields.{key}.max" let:uniqueId>
                    <label for={uniqueId}>
                        <span class="txt">{$_("common.input.maxLength.name")}</span>
                        <i
                            class="ri-information-line link-hint"
                            use:tooltip={$_("common.input.maxLength.tip")}
                        />
                    </label>
                    <input
                        type="number"
                        id={uniqueId}
                        step="1"
                        placeholder={$_("common.input.maxLength.placeholder", {
                            values: { maxLength: maxLength },
                        })}
                        min={field.min || 0}
                        value={field.max || ""}
                        on:input={(e) => (field.max = e.target.value << 0)}
                    />
                </Field>
            </div>

            <div class="col-sm-6">
                <Field class="form-field" name="fields.{key}.pattern" let:uniqueId>
                    <label for={uniqueId}>
                        <span class="txt">{$_("common.input.validationPattern.name")}</span>
                        <i
                            class="ri-information-line link-hint"
                            use:tooltip={{
                                text: $_("common.input.validationPattern.tip"),
                            }}
                        /></label
                    >
                    <input
                        type="text"
                        placeholder={$_("common.input.validationPattern.tip")}
                        id={uniqueId}
                        bind:value={field.pattern}
                    />
                </Field>
            </div>

            <div class="col-sm-6">
                <Field class="form-field" name="fields.{key}.pattern" let:uniqueId>
                    <label for={uniqueId}>
                        <span class="txt">{$_("common.input.autoGeneratePattern.name")}</span>
                        <i
                            class="ri-information-line link-hint"
                            use:tooltip={$_("common.input.autoGeneratePattern.tip")}
                        />
                    </label>
                    <input
                        type="text"
                        placeholder={$_("common.input.autoGeneratePattern.tip")}
                        id={uniqueId}
                        bind:value={field.autogeneratePattern}
                    />
                    <div class="help-block">
                        <p>Ex. <code>{"[a-z0-9]{30}"}</code></p>
                    </div>
                </Field>
            </div>
        </div>
    </svelte:fragment>
</SchemaField>
