import polars as pl

from ..defintion import SDG, DepEdge, DepKind, Indicator, PlaceKind, PlaceNode


def calc_metric_min_max(df: pl.DataFrame) -> tuple[float, float]:
    col = pl.col("value")
    df = df.select(min=col.min(), max=col.max())
    min_value, max_value = df.row(0)
    assert isinstance(min_value, float)
    assert isinstance(max_value, float)
    return min_value, max_value


def is_constant_metric(df: pl.DataFrame) -> bool:
    min_value, max_value = calc_metric_min_max(df)
    return (max_value - min_value) < 1e-8


def replace_enum_values(col_name: str, values: list[str], *, start: int) -> pl.Expr:
    return (
        pl.col(col_name)
        .cast(pl.Enum(values))
        .replace_strict({value: i for i, value in enumerate(values, start=start)})
        .cast(pl.Float64)
    )


def add_node_opt(sdg: SDG, kind: PlaceKind, self_name: str | None) -> PlaceNode | None:
    if self_name is None:
        return None
    return sdg.add_node(PlaceNode(kind=kind, self_name=self_name), strict=False)
