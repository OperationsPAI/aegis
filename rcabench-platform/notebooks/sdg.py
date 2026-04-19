#!/usr/bin/env -S uv run marimo edit

import marimo

__generated_with = "0.14.9"
app = marimo.App(width="full", app_title="SDG Visualization")


@app.cell
def _():
    import json

    import marimo as mo
    import polars as pl

    return json, mo, pl


@app.cell
def _():
    from rcabench_platform.v2.sdk.datasets.spec import (
        get_datapack_folder,
        get_dataset_meta_file,
        read_dataset_index,
    )
    from rcabench_platform.v2.sdk.graphs.sdg.defintion import (
        SDG,
        DepEdge,
        DepKind,
        PlaceKind,
        PlaceNode,
    )

    return get_datapack_folder, get_dataset_meta_file, read_dataset_index


@app.cell
def _(mo):
    mo.md(r"""# SDG Visualization""")
    return


@app.cell
def _(mo):
    all_datasets = [
        "rcabench_filtered",
        "rcabench",
        "rcaeval_re2_tt",
        "rcaeval_re2_ob",
    ]
    dataset_dropdown = mo.ui.dropdown(
        options=all_datasets,
        searchable=True,
        label="dataset",
        value=all_datasets[0],
    )
    mo.output.append(dataset_dropdown)
    return (dataset_dropdown,)


@app.cell
def _(dataset_dropdown, get_dataset_meta_file, mo, pl, read_dataset_index):
    dataset = dataset_dropdown.value
    _index_df = read_dataset_index(dataset)

    _attributes_df_path = get_dataset_meta_file(dataset, "attributes.parquet")
    if dataset.startswith("rcabench") and _attributes_df_path.exists():
        attributes_df = pl.read_parquet(_attributes_df_path)
        _df = attributes_df.select(
            "datapack",
            "inject_time",
            "injection.fault_type",
            "files.total_size:MiB",
        )
    else:
        attributes_df = _index_df
        _df = attributes_df.select("datapack")

    datapack_table = mo.ui.table(_df, selection="single")
    mo.output.append(datapack_table)
    return attributes_df, datapack_table, dataset


@app.cell
def _(datapack_table, dataset, get_datapack_folder, mo):
    from rcabench_platform.v2.sdk.graphs.sdg.build_ import build_sdg
    from rcabench_platform.v2.sdk.graphs.sdg.statistics import calc_statistics

    datapack = datapack_table.value[0, "datapack"]
    mo.stop(
        not isinstance(datapack, str),
        mo.md(f"## {mo.icon('ant-design:warning-outlined')} Please select a datapack"),
    )

    mo.output.append("Building SDG ...")
    sdg = build_sdg(dataset, datapack, get_datapack_folder(dataset, datapack))
    calc_statistics(sdg)
    mo.output.append("Done!")

    mo.output.append({"|V|": sdg.num_nodes(), "|E|": sdg.num_edges()})
    return datapack, sdg


@app.cell
def _(mo):
    neo4j_button = mo.ui.run_button(label="Export SDG to Neo4j")
    mo.output.append(neo4j_button)
    return (neo4j_button,)


@app.cell
def _(mo, neo4j_button, sdg):
    from rcabench_platform.v2.sdk.graphs.sdg.neo4j import export_sdg_to_neo4j

    if neo4j_button.value:
        mo.output.append("Exporting SDG to Neo4j ...")
        export_sdg_to_neo4j(sdg)
        mo.output.append("Done!")
        mo.output.append(mo.md("<http://localhost:7474/browser/>"))
    return


@app.cell
def _(mo):
    mo.md(r"""## Info""")
    return


@app.cell
def _(datapack, dataset, get_datapack_folder, json, mo):
    from rcabench_platform.v2.sdk.datasets.rcabench import rcabench_fix_injection

    mo.stop(not isinstance(datapack, str))
    _datapack_folder = get_datapack_folder(dataset, datapack)
    _injection_path = _datapack_folder / "injection.json"
    mo.stop(not _injection_path.exists())
    with open(_injection_path) as f:
        _injection = json.load(f)
        rcabench_fix_injection(_injection)
    mo.output.append(mo.md("### Injection"))
    mo.output.append(_injection)
    return


@app.cell
def _(attributes_df, datapack, json, mo, pl):
    mo.stop(not isinstance(datapack, str))
    _info = attributes_df.row(by_predicate=pl.col("datapack") == datapack, named=True)
    _info["injection.display_config"] = json.loads(_info["injection.display_config"])
    mo.output.append(mo.md("### Attributes"))
    mo.output.append(_info)
    return


@app.cell
def _(mo, sdg):
    mo.output.append(mo.md("### SDG data"))
    mo.output.append(sdg.data)
    return


@app.cell
def _(datapack, dataset, get_datapack_folder, mo, pl):
    _conclusion_df_path = get_datapack_folder(dataset, datapack) / "conclusion.parquet"
    mo.stop(not _conclusion_df_path.exists())
    _conclusion_df = pl.read_parquet(_conclusion_df_path)
    mo.output.append(mo.md("### Detector conclusions"))
    mo.output.append(_conclusion_df)
    return


@app.cell
def _(mo):
    mo.md(r"""## Places and Indicators""")
    return


@app.cell
def _(mo, sdg):
    from rcabench_platform.v2.sdk.graphs.sdg.dump import dump_place_indicators

    _df = dump_place_indicators(sdg)

    mo.output.append(
        mo.ui.tabs(
            {
                "DataTable": _df,
                "DataExplorer": mo.ui.data_explorer(_df),
                "DataFrame": mo.ui.dataframe(_df, page_size=20),
            }
        )
    )

    all_node_names = [node.uniq_name for node in sdg.iter_nodes()]
    return (all_node_names,)


@app.cell
def _(mo):
    mo.md(r"""## Query: Indicators of Single PlaceNode""")
    return


@app.cell
def _(all_node_names, mo):
    query_indicators_of_node = mo.ui.dropdown(
        options=all_node_names,
        searchable=True,
        label="PlaceNode",
        allow_select_none=True,
    )
    mo.output.append(query_indicators_of_node)
    return (query_indicators_of_node,)


@app.cell
def _(mo, query_indicators_of_node, sdg):
    from rcabench_platform.v2.sdk.graphs.sdg.dump import dump_node_data

    _node = sdg.query_node_by_uniq_name(query_indicators_of_node.value)
    mo.stop(_node is None)

    mo.output.append(_node.uniq_name)
    mo.output.append(dump_node_data(_node))
    for _indicator in _node.indicators.values():
        mo.output.append(_indicator.name)
        mo.output.append(_indicator.data)
        mo.output.append(_indicator.df)
    return


@app.cell
def _(mo):
    mo.md(r"""## Query: Pair of PlaceNodes""")
    return


@app.cell
def _(mo):
    get_query, set_query = mo.state([None, None])
    return get_query, set_query


@app.cell
def _(all_node_names, get_query, mo, set_query):
    query_by_place_node_1 = mo.ui.dropdown(
        options=all_node_names,
        searchable=True,
        label="PlaceNode (1)",
        value=get_query()[0],
        on_change=lambda x: set_query(lambda q: [x, q[1]]),
        allow_select_none=True,
    )
    mo.output.append(query_by_place_node_1)

    query_by_place_node_2 = mo.ui.dropdown(
        options=all_node_names,
        searchable=True,
        label="PlaceNode (2)",
        value=get_query()[1],
        on_change=lambda x: set_query(lambda q: [q[0], x]),
        allow_select_none=True,
    )
    mo.output.append(query_by_place_node_2)

    shift_up_button = mo.ui.run_button(label="Shift Up")
    shift_down_button = mo.ui.run_button(label="Shift Down")
    mo.output.append(shift_up_button)
    mo.output.append(shift_down_button)
    return (
        query_by_place_node_1,
        query_by_place_node_2,
        shift_down_button,
        shift_up_button,
    )


@app.cell
def _(set_query, shift_down_button, shift_up_button):
    if shift_up_button.value:
        set_query(lambda q: [q[1], None])
    if shift_down_button.value:
        set_query(lambda q: [None, q[0]])
    return


@app.cell
def _(mo, pl, query_by_place_node_1, query_by_place_node_2, sdg):
    def dump_edge(sdg, edge):
        src = sdg.get_node_by_id(edge.src_id)
        dst = sdg.get_node_by_id(edge.dst_id)
        return {
            "src": src.uniq_name,
            "kind": edge.kind,
            "dst": dst.uniq_name,
        }

    def show_edges(query, label):
        node_name = query.value
        node = sdg.query_node_by_uniq_name(node_name)

        if not node:
            return

        in_edges = [dump_edge(sdg, e) for e in sdg.in_edges(node.id)]
        out_edges = [dump_edge(sdg, e) for e in sdg.out_edges(node.id)]

        mo.output.append(mo.md(f"### In Edges {label}"))
        mo.output.append(pl.DataFrame(in_edges))

        mo.output.append(mo.md(f"### Out Edges {label}"))
        mo.output.append(pl.DataFrame(out_edges))

    show_edges(query_by_place_node_1, "(1)")
    show_edges(query_by_place_node_2, "(2)")
    return


@app.cell
def _(mo, query_by_place_node_1, query_by_place_node_2, sdg):
    from rcabench_platform.v2.sdk.graphs.sdg.dump import dump_edge_data

    _node1 = sdg.query_node_by_uniq_name(query_by_place_node_1.value)
    _node2 = sdg.query_node_by_uniq_name(query_by_place_node_2.value)
    mo.stop(_node1 is None or _node2 is None)

    mo.output.append(mo.md("### Connected Edges"))
    for edge in sdg.iter_edges_between(_node1, _node2):
        mo.output.append(mo.md(f"#### {edge.kind}"))
        mo.output.append(dump_edge_data(edge))
    return


@app.cell
def _(mo):
    query_span_tree = mo.ui.text(label="trace_id", full_width=True)
    mo.output.append(mo.md("### Query: Span Tree"))
    mo.output.append(query_span_tree)
    return (query_span_tree,)


@app.cell
def _(
    mo,
    pl,
    query_by_place_node_1,
    query_by_place_node_2,
    query_span_tree,
    sdg,
):
    from rcabench_platform.v2.sdk.graphs.sdg.dump import dump_span_tree

    _traces = []

    _node1 = sdg.query_node_by_uniq_name(query_by_place_node_1.value)
    if _node1:
        if _node1.kind == "function":
            _indicator = _node1.indicators.get("duration")
            if _indicator:
                _df = _indicator.df.sort(by="value", descending=True)
                if query_span_tree.value:
                    _df = _df.filter(pl.col("trace_id") == query_span_tree.value)
                mo.output.append(mo.md("### Traces: Duration (1)"))
                mo.output.append(_df)
                _traces.append(_df)

    _node2 = sdg.query_node_by_uniq_name(query_by_place_node_2.value)
    if _node2:
        if _node2.kind == "function":
            _indicator = _node2.indicators.get("duration")
            if _indicator:
                _df = _indicator.df.sort(by="value", descending=True)
                if query_span_tree.value:
                    _df = _df.filter(pl.col("trace_id") == query_span_tree.value)
                mo.output.append(mo.md("### Traces: Duration (2)"))
                mo.output.append(_df)
                _traces.append(_df)

    if len(_traces) == 2:
        _df1, _df2 = _traces

        s1 = set(_df1["trace_id"].unique())
        s2 = set(_df2["trace_id"].unique())
        common_trace_ids = s1 & s2

        _df = pl.DataFrame({"trace_id": list(common_trace_ids)})

        _df = (
            _df.join(
                _df1.group_by("trace_id").agg(
                    pl.col("anomal").max().alias("anomal_1"),
                    pl.col("value").max().alias("value_1"),
                    pl.col("time").min().alias("time_1"),
                ),
                on="trace_id",
                how="left",
            )
            .join(
                _df2.group_by("trace_id").agg(
                    pl.col("anomal").max().alias("anomal_2"),
                    pl.col("value").max().alias("value_2"),
                    pl.col("time").min().alias("time_2"),
                ),
                on="trace_id",
                how="left",
            )
            .select(
                "trace_id",
                "value_1",
                "value_2",
                "time_1",
                "time_2",
                "anomal_1",
                "anomal_2",
            )
            .sort(by="time_1")
        )

        mo.output.append(mo.md("### Common Trace IDs"))
        mo.output.append(_df)

    if _node1 and query_span_tree.value:
        _span_tree = dump_span_tree(sdg, _node1, query_span_tree.value)
        mo.output.append(mo.md("#### Span Tree (1)"))
        mo.output.append(_span_tree)

    if _node2 and query_span_tree.value:
        _span_tree = dump_span_tree(sdg, _node2, query_span_tree.value)
        mo.output.append(mo.md("#### Span Tree (2)"))
        mo.output.append(_span_tree)
    return


@app.cell
def _(mo, pl, query_by_place_node_1, query_by_place_node_2, sdg):
    import plotly.express as px

    def plot_indicator(node, indicator):
        title = f"{node.uniq_name}|{indicator.name}"

        df = indicator.df

        if indicator.name == "duration":
            df = df.with_columns(pl.col("value") / 1e6)

        if node.kind == "function":
            hover_data = ["time", "value", "trace_id", "span_id"]
        else:
            hover_data = None

        fig = px.line(
            df.to_pandas(),
            x="time",
            y="value",
            markers=True,
            hover_data=hover_data,
        )

        fig.add_vline(
            x=sdg.data["inject_time"],
            line_color="red",
            line_width=1,
        )

        fig.update_layout(
            title={
                "text": title,
                "xanchor": "center",
                "yanchor": "top",
                "x": 0.5,
                "y": 0.95,
            },
        )

        if indicator.name == "duration":
            fig.update_yaxes(exponentformat="none", ticksuffix=" ms")  # type:ignore

        return fig

    # ----

    _node1 = sdg.query_node_by_uniq_name(query_by_place_node_1.value)
    _node2 = sdg.query_node_by_uniq_name(query_by_place_node_2.value)

    if _node1 and _node2:
        common_indicators = []
        for indicator in _node1.indicators.values():
            if indicator.name in _node2.indicators:
                common_indicators.append(indicator.name)
    else:
        common_indicators = []

    mo.output.append(mo.md(f"inject_time: `{sdg.data['inject_time']}`"))

    if _node1 and _node2:
        mo.output.append(mo.md("### Indicators (common)"))
        for indicator in common_indicators:
            _fig = plot_indicator(_node1, _node1.indicators[indicator])
            mo.output.append(mo.ui.plotly(_fig))

            _fig = plot_indicator(_node2, _node2.indicators[indicator])
            mo.output.append(mo.ui.plotly(_fig))

    if _node1:
        mo.output.append(mo.md("### Indicators (1)"))
        for indicator in _node1.indicators.values():
            if indicator.name in common_indicators:
                continue
            _fig = plot_indicator(_node1, indicator)
            mo.output.append(mo.ui.plotly(_fig))

    if _node2:
        mo.output.append(mo.md("### Indicators (2)"))
        for indicator in _node2.indicators.values():
            if indicator.name in common_indicators:
                continue
            _fig = plot_indicator(_node2, indicator)
            mo.output.append(mo.ui.plotly(_fig))
    return


if __name__ == "__main__":
    app.run()
